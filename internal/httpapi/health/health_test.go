package health

import "testing"

func TestProfileRunning(t *testing.T) {
	out := `                     Name          Type                                       Data  State
=================================================================================================
         external-ingress       profile                   sip:mod_sofia@172.28.0.10:5060  RUNNING (0)
          external-egress       profile                   sip:mod_sofia@172.28.0.10:5080  RUNNING (0)
carrier-answer::external-egress gateway  sip:gw@172.28.0.61:5060  REGED
=================================================================================================
2 profiles 1 alias`
	if !profileRunning(out, "external-ingress") {
		t.Fatal("expected external-ingress running")
	}
	if profileRunning(out, "missing") {
		t.Fatal("missing profile reported running")
	}
}
