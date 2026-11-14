package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestLoginOwnsNativeTokenAndDoesNotReturnIt(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/LoginRequest" || r.Method != "POST" || r.Header.Get("Authorization") != "" {
			t.Error("wrong native login boundary")
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request["username"] != "fixture-user" || request["password"] != "password-canary" {
			t.Error("credentials changed")
		}
		io.WriteString(w, `{"errors":[],"httpToken":"native-token-canary","properties":{}}`)
	})
	result, err := c.Login(context.Background(), Credentials{Username: "fixture-user", Password: "password-canary", ServerAddress: "console.fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || !c.Authorized() {
		t.Fatal("successful native login missing")
	}
	public, _ := json.Marshal(result)
	if strings.Contains(string(public), "canary") || strings.Contains(string(public), "httpToken") {
		t.Fatal("native token exposed")
	}
}

func TestLoginDoesNotTreatMigrationTokenAsCompletedSetup(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"errors":[],"httpToken":"native-token-canary","selfServiceCheckJson":"{\"status\":\"SELECTION_REQUIRED\",\"devices\":[]}"}`)
	})
	result, err := c.Login(context.Background(), Credentials{Username: "fixture-user", Password: "password-canary", ServerAddress: "console.fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Complete || !c.Authorized() || len(result.MigrationCheck) == 0 {
		t.Fatal("migration stage lost or misreported as complete")
	}
}

func TestLoginRejectsNativeErrorsAndMissingToken(t *testing.T) {
	for _, body := range []string{`{}`, `{"errors":[],"httpToken":""}`, `{"errors":[{"id":"FAILED","private":"canary"}],"httpToken":"rejected-token"}`, `{"errors":[],"httpToken":"bad\ntoken"}`, `{"errors":[],"httpToken":"ok","selfServiceCheckJson":"not-json"}`} {
		t.Run(body, func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
			if _, err := c.Login(context.Background(), Credentials{Username: "fixture-user", Password: "password-canary", ServerAddress: "console.fixture.invalid"}); err == nil || strings.Contains(err.Error(), "canary") {
				t.Fatal("invalid login accepted or private body exposed")
			}
			if c.Authorized() {
				t.Fatal("unaccepted native token retained")
			}
		})
	}
}

func TestLoginDecodesNativeBusinessErrorsWithoutExposingBodies(t *testing.T) {
	for _, status := range []int{200, 400, 500, 503} {
		for _, body := range []string{
			`{"errors":[{"id":"INVALID_PASSWORD","message":"credential-canary"}]}`,
			`{"LoginResponseMessage":{"errors":[{"id":"INVALID_PASSWORD","message":"credential-canary"}]},"OtherResponseMessage":{"errors":[]}}`,
		} {
			t.Run(fmt.Sprint(status, body), func(t *testing.T) {
				calls := 0
				c := fixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(status); io.WriteString(w, body) })
				_, err := c.Login(context.Background(), Credentials{Username: "fixture", Password: "fixture", ServerAddress: "fixture.invalid"})
				if !errors.Is(err, ErrLoginRejected) || errors.Is(err, ErrIndeterminate) || calls != 1 {
					t.Fatalf("business rejection lost: %v, calls=%d", err, calls)
				}
				if strings.Contains(fmt.Sprintf("%+v %#v", err, err), "credential-canary") {
					t.Fatal("private error body exposed")
				}
			})
		}
	}
}

func TestLoginAcceptsNamedResponseAndChecksSiblingErrors(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			extra := `[]`
			if rejected {
				extra = `[{"id":"FAILED"}]`
			}
			io.WriteString(w, `{"LoginResponseMessage":{"errors":[],"httpToken":"native-canary"},"OtherResponseMessage":{"errors":`+extra+`}}`)
		})
		result, err := c.Login(context.Background(), Credentials{Username: "fixture", Password: "fixture", ServerAddress: "fixture.invalid"})
		if rejected {
			if !errors.Is(err, ErrLoginRejected) || c.Authorized() {
				t.Fatal("sibling failure accepted", err)
			}
		} else if err != nil || !result.Complete {
			t.Fatal("named login response rejected", err)
		}
	}
}

func TestSSOUsesNativeBrowserFlowAndFinalizesOnlyOnce(t *testing.T) {
	ready := make(chan *websocket.Conn, 1)
	connected := make(chan struct{})
	completed := make(chan Credentials, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/LoginConfiguration":
			io.WriteString(w, `{"loginType":"cloud_sso"}`)
		case "/v0/wsevent":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			ready <- conn
			close(connected)
			defer conn.CloseNow()
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		case "/v0/SsoAuthInitiateRequest":
			// Accept sends HTTP 101 before this handler publishes the connection.
			// Wait for that publication instead of assuming goroutine scheduling.
			select {
			case <-connected:
			case <-ctx.Done():
				t.Error("SSO started before native event connection")
				return
			}
			io.WriteString(w, `{"errors":[],"ssoLoginUrls":[{"one":"Fixture provider","two":"https://login.fixture.invalid/login?uuid=private-canary"}]}`)
		case "/v0/LoginRequest":
			var in Credentials
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			completed <- in
			io.WriteString(w, `{"errors":[],"httpToken":"native-token-canary"}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	attempt, err := c.BeginSSO(ctx, LoginInfo{Username: "fixture-user", ServerAddress: "console.fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Close()
	if len(attempt.Providers) != 1 || attempt.Providers[0].Name != "Fixture provider" {
		t.Fatal("provider list missing")
	}
	conn := <-ready
	go func() {
		if err := wsjson.Write(ctx, conn, map[string]any{"SsoAuthFinalizeMessage": map[string]string{"username": "fixture-user", "authToken": "sso-private-canary"}}); err != nil {
			t.Error(err)
		}
	}()
	result, err := completeConfirmedSSO(t, attempt, ctx)
	if err != nil || !result.Complete {
		t.Fatal(err)
	}
	request := <-completed
	if request.SSOToken != "sso-private-canary" || len(request.Password) < 20 || request.Username != "fixture-user" {
		t.Fatal("native SSO completion changed")
	}
	if _, err := attempt.Complete(ctx, "stale", "fixture-user"); !errors.Is(err, ErrAlreadyCompleted) {
		t.Fatal("SSO completion may replay")
	}
	if len(completed) != 0 {
		t.Fatal("native login replayed")
	}
}

func TestSSORejectsUnsafeProviderBeforeReturningLink(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/LoginConfiguration" {
			io.WriteString(w, `{"loginType":"cloud_sso"}`)
			return
		}
		if r.URL.Path == "/v0/wsevent" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		}
		io.WriteString(w, `{"errors":[],"ssoLoginUrl":"javascript:alert(1)"}`)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if attempt, err := c.BeginSSO(ctx, LoginInfo{Username: "fixture-user", ServerAddress: "console.fixture.invalid"}); err == nil {
		attempt.Close()
		t.Fatal("unsafe SSO link accepted")
	}
}

func TestCancelledSSODoesNotOverlapAnUnfinishedNativeLogin(t *testing.T) {
	ready := make(chan *websocket.Conn, 1)
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	defer close(release)
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/LoginConfiguration":
			io.WriteString(w, `{"loginType":"cloud_sso"}`)
		case "/v0/wsevent":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			ready <- conn
			defer conn.CloseNow()
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		case "/v0/SsoAuthInitiateRequest":
			io.WriteString(w, `{"ssoLoginUrl":"https://login.fixture.invalid/login"}`)
		case "/v0/LoginRequest":
			entered <- struct{}{}
			<-release
			io.WriteString(w, `{"httpToken":"native-token-canary"}`)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	attempt, err := c.BeginSSO(ctx, LoginInfo{Username: "fixture-user", ServerAddress: "console.fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Close()
	conn := <-ready
	if err := wsjson.Write(ctx, conn, map[string]any{"SsoAuthFinalizeMessage": map[string]string{"username": "fixture-user", "authToken": "sso-canary"}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); completeConfirmedSSO(t, attempt, ctx) }()
	<-entered
	attempt.Close()
	request, cancelRequest := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancelRequest()
	if _, err := c.Login(request, Credentials{Username: "fixture-user", Password: "canary", ServerAddress: "console.fixture.invalid"}); !errors.Is(err, ErrLoginInProgress) {
		t.Fatalf("closing the event connection allowed overlapping native logins: %v", err)
	}
}

func TestRegistrationContextNeverLeaksBetweenAccountsOrAuthorities(t *testing.T) {
	lookups, logins := 0, 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/LoginConfiguration":
			lookups++
			key := r.URL.Query().Get("username") + "@" + r.URL.Query().Get("serverAddress")
			json.NewEncoder(w).Encode(map[string]string{"loginType": "local", "regKey": key})
		case "/v0/LoginRequest":
			logins++
			var in Credentials
			json.NewDecoder(r.Body).Decode(&in)
			if in.RegistrationKey != in.Username+"@"+in.ServerAddress || !in.Register {
				t.Error("registration context crossed account or authority")
			}
			io.WriteString(w, `{"errors":[],"httpToken":"fixture"}`)
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	})
	for _, pair := range [][2]string{{"one", "a.invalid"}, {"two", "a.invalid"}, {"one", "b.invalid"}} {
		if _, err := c.Login(context.Background(), Credentials{Username: pair[0], ServerAddress: pair[1], Password: "fixture", Register: true}); err != nil {
			t.Fatal(err)
		}
	}
	if lookups != 3 || logins != 3 {
		t.Fatal(lookups, logins)
	}
}
func TestRegistrationWithoutNativeKeyNeverFallsBackToLogin(t *testing.T) {
	for _, sso := range []bool{false, true} {
		t.Run(fmt.Sprint(sso), func(t *testing.T) {
			lookups := 0
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/LoginConfiguration" {
					t.Error("registration continued without a key", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				lookups++
				io.WriteString(w, `{"loginType":"local"}`)
			})
			var err error
			if sso {
				_, err = c.BeginSSO(context.Background(), LoginInfo{Username: "new", ServerAddress: "fixture.invalid", Register: true})
			} else {
				_, err = c.Login(context.Background(), Credentials{Username: "new", Password: "fixture", ServerAddress: "fixture.invalid", Register: true})
			}
			if !errors.Is(err, ErrLoginRejected) || lookups != 1 {
				t.Fatal("silent register-to-login fallback", err, lookups)
			}
		})
	}
}

// The desktop app signs in through the browser for local accounts too; the
// CrashPlan page then asks for password and verification code.
func TestBrowserSignInAcceptsLocalAccounts(t *testing.T) {
	for loginType, accepted := range map[string]bool{"local": true, "cloud_sso": true, "unknown": false} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		initiated := false
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/LoginConfiguration":
				io.WriteString(w, `{"loginType":"`+loginType+`"}`)
			case "/v0/wsevent":
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				for {
					if _, _, err := conn.Read(r.Context()); err != nil {
						return
					}
				}
			case "/v0/SsoAuthInitiateRequest":
				initiated = true
				io.WriteString(w, `{"errors":[],"ssoLoginUrls":[{"one":"CrashPlan","two":"https://login.fixture.invalid/login?uuid=x"}]}`)
			default:
				w.WriteHeader(404)
			}
		})
		attempt, err := c.BeginSSO(ctx, LoginInfo{Username: "fixture-user", ServerAddress: "console.fixture.invalid"})
		if accepted && (err != nil || !initiated || len(attempt.Providers) != 1) {
			t.Fatal(loginType, err)
		}
		if !accepted && !errors.Is(err, ErrProtocol) {
			t.Fatal(loginType, "accepted", err)
		}
		if attempt != nil {
			attempt.Close()
		}
		cancel()
	}
}
