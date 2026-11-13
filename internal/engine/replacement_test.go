package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestReplacementWorkflowCanBeFinishedOrCancelledWithReadback(t *testing.T) {
	for _, mode := range []string{"finish", "cancel", "unconfirmed", "inactive", "lost", "unchanged"} {
		t.Run(mode, func(t *testing.T) {
			writes := 0
			active := mode != "inactive"
			c := identityFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					writes++
					var body map[string]bool
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["inProgress"] {
						t.Error("unbounded native replacement update")
					}
					if mode != "unchanged" {
						active = false
					}
					if mode == "lost" {
						w.WriteHeader(500)
						return
					}
					return
				}
				if active {
					io.WriteString(w, `{"available":true,"inProgress":true,"deviceSelectedGuid":"9","screenState":"/replace-device/complete"}`)
				} else {
					io.WriteString(w, `{"available":false,"inProgress":false,"deviceSelectedGuid":"null"}`)
				}
			})
			c.SetToken("fixture")
			action := "finish"
			if mode == "cancel" {
				action = mode
			}
			confirmation := "FINISH REPLACEMENT"
			if action == "cancel" {
				confirmation = "CANCEL REPLACEMENT"
			}
			if mode == "unconfirmed" {
				confirmation = ""
			}
			state, err := c.CompleteReplacement(context.Background(), action, confirmation)
			if mode == "finish" || mode == "cancel" {
				if err != nil || state.InProgress || state.Selected != "" || writes != 1 {
					t.Fatal(state, err, writes)
				}
			} else if err == nil {
				t.Fatal("invalid or uncertain replacement completion accepted")
			}
			if (mode == "lost" || mode == "unchanged") && !errors.Is(err, ErrIndeterminate) {
				t.Fatal("lost completion was not indeterminate", err)
			}
			if (mode == "unconfirmed" || mode == "inactive") && writes != 0 {
				t.Fatal("unexpected reset", writes)
			}
		})
	}
}
