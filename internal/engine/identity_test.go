package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestSecurityUpgradePinsNativeAccountWithAnUnchangedToken(t *testing.T) {
	for _, phase := range []string{"preflight", "dispatched"} {
		t.Run(phase, func(t *testing.T) {
			var identity atomic.Int32
			identity.Store(1)
			var writes atomic.Int32
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v0/GetComputers":
					fmt.Fprintf(w, `{"UsersMessage":{"myUserId":"%d"},"ComputersMessage":{"computers":[{"peerId":"7","userId":"%d","properties":{"ME":"true"}}]}}`, identity.Load(), identity.Load())
				case "/v1/DataKey":
					if writes.Load() == 0 {
						if phase == "preflight" {
							identity.Store(2)
						}
						io.WriteString(w, `{"securityKeyType":"AccountPassword"}`)
					} else {
						io.WriteString(w, `{"securityKeyType":"PrivatePassword"}`)
					}
				case "/v0/SecurityKeyTypeRequest":
					writes.Add(1)
					identity.Store(2)
					io.WriteString(w, `{"errors":[]}`)
				}
			})
			c.SetToken("unchanged-token")
			_, err := c.UpgradeSecurity(context.Background(), SecurityUpgrade{Type: "PrivatePassword", Confirm: "UPGRADE TO PrivatePassword", Password: "fixture-password"})
			if phase == "preflight" {
				if !errors.Is(err, ErrSessionChanged) || !errors.Is(err, ErrNotDispatched) || writes.Load() != 0 {
					t.Fatal("account changed before dispatch", err, writes.Load())
				}
			} else if !errors.Is(err, ErrIndeterminate) || writes.Load() != 1 {
				t.Fatal("account change after dispatch accepted", err, writes.Load())
			}
		})
	}
}

func TestDeviceReplacementAllowsOnlyItsConfirmedIdentityTransition(t *testing.T) {
	for _, mode := range []string{"confirmed", "other-device", "other-user"} {
		t.Run(mode, func(t *testing.T) {
			var self, user atomic.Int32
			self.Store(7)
			user.Store(1)
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v0/GetComputers":
					fmt.Fprintf(w, `{"UsersMessage":{"myUserId":"%d"},"ComputersMessage":{"computers":[{"peerId":"%d","userId":"%d","properties":{"ME":"true"}},{"peerId":"9","userId":"1","source":true}]}}`, user.Load(), self.Load(), user.Load())
				case "/v0/ReplaceDeviceRequest":
					self.Store(9)
					if mode == "other-device" {
						self.Store(10)
					}
					if mode == "other-user" {
						user.Store(2)
					}
					io.WriteString(w, `{"errors":[]}`)
				}
			})
			c.SetToken("fixture")
			revision := c.SessionRevision()
			err := c.ReplaceDevice(context.Background(), "9", "9")
			if mode == "confirmed" {
				if err != nil || c.SessionRevision() == revision {
					t.Fatal("confirmed transition not recognized", err)
				}
			} else if !errors.Is(err, ErrIndeterminate) {
				t.Fatal("unconfirmed native identity accepted", err)
			}
		})
	}
}
