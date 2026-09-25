package telegram

import (
	"context"
	"testing"
)

func TestInspectRuntimeVerified(t *testing.T) {
	native := newFakeNative()
	native.executeResults[`{"@type":"getOption","name":"version"}`] = []byte(`{"@type":"optionValueString","value":"1.8.0"}`)
	native.executeResults[`{"@type":"getOption","name":"commit_hash"}`] = []byte(`{"@type":"optionValueString","value":"abc"}`)
	info, err := InspectRuntime(context.Background(), native, CompatibilityManifest{Version: "1.8.0", Commit: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != CompatibilityVerified {
		t.Fatalf("mode = %s", info.Mode)
	}
}

func TestInspectRuntimeRejectedOnCommitMismatch(t *testing.T) {
	native := newFakeNative()
	native.executeResults[`{"@type":"getOption","name":"version"}`] = []byte(`{"@type":"optionValueString","value":"1.8.0"}`)
	native.executeResults[`{"@type":"getOption","name":"commit_hash"}`] = []byte(`{"@type":"optionValueString","value":"actual"}`)
	info, err := InspectRuntime(context.Background(), native, CompatibilityManifest{Commit: "expected"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != CompatibilityRejected {
		t.Fatalf("mode = %s", info.Mode)
	}
}
