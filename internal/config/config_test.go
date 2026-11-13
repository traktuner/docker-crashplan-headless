package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHeadlessNeedsLocalRootsButNoWebPassword(t *testing.T) {
	env := map[string]string{"CPH_SOURCE_ROOTS": `["/data"]`}
	c, err := Load(func(key string) string { return env[key] })
	if err != nil || c.Mode != "headless" || c.Listen != "" {
		t.Fatal("headless opened TCP or refused local roots", err)
	}
	env["CPH_MODE"] = "web"
	if _, err := Load(func(key string) string { return env[key] }); err == nil {
		t.Fatal("web mode without password accepted")
	}
}

func TestConfigMountCannotBeRedirectedAwayFromImageLayout(t *testing.T) {
	for _, root := range []string{"", "/config", "/other"} {
		env := map[string]string{"CPH_SOURCE_ROOTS": `["/data"]`, "CPH_CONFIG_DIR": root}
		c, err := Load(func(key string) string { return env[key] })
		if root == "/other" {
			if err == nil {
				t.Fatal("accepted a config path that the image symlinks do not use")
			}
		} else if err != nil || c.StateDir != "/config/cph" {
			t.Fatal("standard config layout refused", err)
		}
	}
}

func TestWebPasswordFileAndStrictEnvironment(t *testing.T) {
	password := filepath.Join(t.TempDir(), "password")
	os.WriteFile(password, []byte("fixture-web-password-12345\n"), 0600)
	env := map[string]string{"CPH_SOURCE_ROOTS": `["/data"]`, "CPH_MODE": "web", "CPH_WEB_PASSWORD_FILE": password, "CPH_PUBLIC_ORIGINS": `["https://backup.fixture.invalid"]`}
	get := func(key string) string { return env[key] }
	if c, err := Load(get); err != nil || c.WebPassword != "fixture-web-password-12345" || c.Listen != ":8080" {
		t.Fatal("web configuration failed", err)
	}
	for key, value := range map[string]string{"CPH_MODE": "WEB", "CPH_LOG_LEVEL": "secret", "CPH_SOURCE_ROOTS": `["/"]`, "CPH_RESTORE_ROOTS": "/restore", "CPH_PUBLIC_ORIGINS": ""} {
		old := env[key]
		env[key] = value
		if _, err := Load(get); err == nil {
			t.Errorf("bad %s accepted", key)
		}
		env[key] = old
	}
}
