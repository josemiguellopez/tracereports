package env

import "testing"

func TestGetAndBool(t *testing.T) {
	t.Setenv("TRACEREPORTS_TOKEN", "tok")
	if Get("TOKEN") != "tok" {
		t.Fatal("Get reads TRACEREPORTS_<name>")
	}
	for v, want := range map[string]bool{"true": true, " YES ": true, "1": true, "false": false, "": false, "0": false} {
		t.Setenv("TRACEREPORTS_SETTINGS_LOCKED", v)
		if Bool("SETTINGS_LOCKED") != want {
			t.Errorf("Bool(%q) = %v", v, !want)
		}
	}
}
