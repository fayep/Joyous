package inkjoybridge

import "testing"

func TestDefaultAllowListsPassWaveformUpdates(t *testing.T) {
	if !DefaultDownstreamAllow().Allows("wf_update") {
		t.Error("cloud wf_update must reach the frame")
	}
	if !DefaultUpstreamAllow().Allows("wf_update_ack") {
		t.Error("frame wf_update_ack must reach the cloud")
	}
	if DefaultIntercept().Allows("wf_update") {
		t.Error("wf_update must not be intercepted")
	}
}
