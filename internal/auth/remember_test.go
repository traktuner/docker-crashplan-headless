package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/traktuner/crashplan-headless/internal/statefile"
)

type memoryStore struct {
	mu      sync.Mutex
	files   map[string][]byte
	writes  int
	failing bool
}

func (s *memoryStore) Read(name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), raw...), nil
}

func (s *memoryStore) Write(name string, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failing {
		return errors.New("PRIVATE_STATE_UNAVAILABLE")
	}
	if s.files == nil {
		s.files = map[string][]byte{}
	}
	s.files[name] = append([]byte(nil), raw...)
	s.writes++
	return nil
}

const (
	rememberUser     = "operator"
	rememberPassword = "fixture-password-12345"
	rememberOrigin   = "https://backup.fixture.invalid"
)

func persistentManager(t *testing.T, store Store, password string, clock *time.Time, report func(error)) *Manager {
	t.Helper()
	m, err := NewPersistent(rememberUser, password, []string{rememberOrigin}, 30*time.Minute, store, report)
	if err != nil {
		t.Fatal(err)
	}
	if clock != nil {
		m.now = func() time.Time { return *clock }
	}
	return m
}

func readRequest(m *Manager, s Session) *http.Request {
	r := httptest.NewRequest("GET", rememberOrigin+"/api/v1/state", nil)
	r.AddCookie(m.Cookie(s))
	return r
}

func TestRememberedSessionSlidesForDaysButPlainSessionDoesNot(t *testing.T) {
	now := time.Now()
	store := &memoryStore{}
	m := persistentManager(t, store, rememberPassword, &now, nil)
	plain, err := m.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, false)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := m.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := m.Cookie(kept); !c.Expires.After(now.Add(80 * 24 * time.Hour)) {
		t.Fatalf("remembered cookie ends too early: %v", c.Expires)
	}
	// Visit every 20 days for 60 days: the remembered session stays, the plain one is gone.
	for i := 0; i < 3; i++ {
		now = now.Add(20 * 24 * time.Hour)
		if _, err := m.Authorize(readRequest(m, kept), false); err != nil {
			t.Fatalf("remembered session ended early after %d visits: %v", i+1, err)
		}
	}
	if _, err := m.Authorize(readRequest(m, plain), false); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("plain session outlived its 12 hours")
	}
	// The 90-day limit is absolute, however often it is used.
	now = now.Add(20 * 24 * time.Hour)
	if _, err := m.Authorize(readRequest(m, kept), false); err != nil {
		t.Fatal("remembered session ended before 90 days", err)
	}
	now = now.Add(20 * 24 * time.Hour)
	if _, err := m.Authorize(readRequest(m, kept), false); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("remembered session outlived 90 days")
	}
}

func TestRememberedSessionSurvivesRestartAndPlainOneDoesNot(t *testing.T) {
	now := time.Now()
	store := &memoryStore{}
	first := persistentManager(t, store, rememberPassword, &now, nil)
	plain, _ := first.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, false)
	kept, err := first.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, true)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(store.files[persistFile])
	for _, secret := range []string{kept.ID, plain.ID, rememberPassword} {
		if strings.Contains(raw, secret) {
			t.Fatal("saved sessions contain a secret value")
		}
	}
	if strings.Count(raw, `"k"`) != 1 {
		t.Fatalf("only the remembered session is saved: %s", raw)
	}

	now = now.Add(3 * 24 * time.Hour) // a restart days later
	second := persistentManager(t, store, rememberPassword, &now, nil)
	r := readRequest(second, kept)
	got, err := second.Authorize(r, false)
	if err != nil || got.ID != kept.ID || got.CSRF != kept.CSRF {
		t.Fatal("remembered session lost after restart", err)
	}
	if _, err := second.Authorize(readRequest(second, plain), false); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("plain session survived a restart")
	}
	// A mutation still needs the saved CSRF token.
	post := httptest.NewRequest("POST", rememberOrigin+"/api/v1/scan", nil)
	post.AddCookie(second.Cookie(kept))
	post.Header.Set("Origin", rememberOrigin)
	if _, err := second.Authorize(post, true); !errors.Is(err, ErrCSRF) {
		t.Fatal("mutation without CSRF token accepted", err)
	}
	post.Header.Set("X-CSRF-Token", kept.CSRF)
	if _, err := second.Authorize(post, true); err != nil {
		t.Fatal("mutation with the saved CSRF token rejected", err)
	}
}

func TestSignOutEndsSavedSessionForGood(t *testing.T) {
	now := time.Now()
	store := &memoryStore{}
	m := persistentManager(t, store, rememberPassword, &now, nil)
	kept, _ := m.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, true)
	other, _ := m.LoginRemember(context.Background(), "192.0.2.2", rememberUser, rememberPassword, true)
	m.Logout(kept.ID)
	restarted := persistentManager(t, store, rememberPassword, &now, nil)
	if _, err := restarted.Authorize(readRequest(restarted, kept), false); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("signed-out session came back after a restart")
	}
	if _, err := restarted.Authorize(readRequest(restarted, other), false); err != nil {
		t.Fatal("signing out one device ended another", err)
	}
}

func TestChangedPasswordOrUserEndsSavedSessions(t *testing.T) {
	now := time.Now()
	store := &memoryStore{}
	m := persistentManager(t, store, rememberPassword, &now, nil)
	kept, _ := m.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, true)
	changed := persistentManager(t, store, "another-fixture-password-1", &now, nil)
	if _, err := changed.Authorize(readRequest(changed, kept), false); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("session survived a password change")
	}
	m2, err := NewPersistent("someone-else", rememberPassword, []string{rememberOrigin}, 30*time.Minute, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Authorize(readRequest(m2, kept), false); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("session survived a user name change")
	}
}

func TestExpiredAndBrokenSavedSessionsAreIgnored(t *testing.T) {
	now := time.Now()
	store := &memoryStore{}
	m := persistentManager(t, store, rememberPassword, &now, nil)
	kept, _ := m.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, true)
	late := now.Add(91 * 24 * time.Hour)
	expired := persistentManager(t, store, rememberPassword, &late, nil)
	if _, err := expired.Authorize(readRequest(expired, kept), false); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired saved session accepted")
	}

	var reported []error
	for _, garbage := range []string{"not json", `{"v":2,"s":[]}`, `{"v":1,"s":[{"k":"short","c":"x","t":1,"e":9999999999,"a":9999999999}]}`} {
		broken := &memoryStore{files: map[string][]byte{persistFile: []byte(garbage)}}
		failed := persistentManager(t, broken, rememberPassword, &now, func(err error) { reported = append(reported, err) })
		if _, err := failed.Authorize(readRequest(failed, kept), false); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("broken saved sessions accepted")
		}
		if _, err := failed.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, false); err != nil {
			t.Fatal("broken saved sessions blocked sign-in", err)
		}
	}
	if len(reported) != 2 {
		t.Fatalf("unreadable files must be reported (2 expected, the third only has bad items): %d", len(reported))
	}
}

func TestSaveFailureIsReportedAndSignInStillWorks(t *testing.T) {
	now := time.Now()
	store := &memoryStore{failing: true}
	var reported int
	m := persistentManager(t, store, rememberPassword, &now, func(error) { reported++ })
	kept, err := m.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, true)
	if err != nil {
		t.Fatal("a save problem blocked the sign-in", err)
	}
	if reported == 0 {
		t.Fatal("a save problem was not reported")
	}
	if _, err := m.Authorize(readRequest(m, kept), false); err != nil {
		t.Fatal("session unusable after a save problem", err)
	}
}

func TestSlidingIsSavedOnlyNowAndThen(t *testing.T) {
	now := time.Now()
	store := &memoryStore{}
	m := persistentManager(t, store, rememberPassword, &now, nil)
	kept, _ := m.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, true)
	base := store.writes
	for i := 0; i < 20; i++ {
		now = now.Add(10 * time.Minute)
		if _, err := m.Authorize(readRequest(m, kept), false); err != nil {
			t.Fatal(err)
		}
	}
	if store.writes != base {
		t.Fatalf("every request wrote the file: %d writes", store.writes-base)
	}
	now = now.Add(7 * time.Hour)
	if _, err := m.Authorize(readRequest(m, kept), false); err != nil {
		t.Fatal(err)
	}
	if store.writes != base+1 {
		t.Fatalf("the slid expiry was not saved: %d writes", store.writes-base)
	}
}

func TestPlainSessionsNeverTouchTheStoreAndPlainEvictionOfSavedOneIsSaved(t *testing.T) {
	now := time.Now()
	store := &memoryStore{}
	m := persistentManager(t, store, rememberPassword, &now, nil)
	if _, err := m.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, false); err != nil {
		t.Fatal(err)
	}
	if store.writes != 0 {
		t.Fatal("a plain sign-in wrote the store")
	}
	kept, _ := m.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, true)
	// Fill the table; the oldest sessions are evicted, the saved list follows.
	for i := 0; i < maxSessions; i++ {
		now = now.Add(time.Second)
		if _, err := m.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, false); err != nil {
			t.Fatal(err)
		}
	}
	restarted := persistentManager(t, store, rememberPassword, &now, nil)
	_, inMemory := m.sessions[m.sid(kept.ID)]
	_, afterRestart := restarted.sessions[restarted.sid(kept.ID)]
	if inMemory != afterRestart {
		t.Fatalf("saved sessions differ from memory: memory %v, restart %v", inMemory, afterRestart)
	}
}

// The real private store accepts the file name and the saved list round-trips.
func TestRememberedSessionRoundTripsThroughThePrivateStateStore(t *testing.T) {
	dir := t.TempDir() + "/cph"
	store, err := statefile.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var problems []error
	report := func(err error) { problems = append(problems, err) }
	first := persistentManager(t, store, rememberPassword, nil, report)
	kept, err := first.LoginRemember(context.Background(), "192.0.2.1", rememberUser, rememberPassword, true)
	if err != nil {
		t.Fatal(err)
	}
	second := persistentManager(t, store, rememberPassword, nil, report)
	if _, err := second.Authorize(readRequest(second, kept), false); err != nil {
		t.Fatal("saved session not restored from the private store", err)
	}
	if len(problems) != 0 {
		t.Fatalf("private store problems: %v", problems)
	}
}
