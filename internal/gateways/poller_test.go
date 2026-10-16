package gateways

import "testing"

// FreeSWITCH answers an empty gwlist with "-ERR no reply"; a wrong profile
// answers "Invalid Profile [...]". Only the first means "none in this state".
func TestEmptyGatewayList(t *testing.T) {
	for _, s := range []string{"-ERR no reply", "esl: -ERR no reply", "No Reply"} {
		if !emptyGatewayList(s) {
			t.Errorf("%q should read as an empty list", s)
		}
	}
	for _, s := range []string{"Invalid Profile [bogus]", "gw1 gw2", "-ERR invalid profile", ""} {
		if emptyGatewayList(s) {
			t.Errorf("%q must not read as an empty list", s)
		}
	}
}
