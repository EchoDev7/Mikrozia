package runtime

import (
	"strings"
	"testing"
)

func TestSanitizeStreamSettingsForRemote_PreservesTcpCongestionBbr(t *testing.T) {
	input := `{"network":"tcp","sockopt":{"tcpcongestion":"bbr","tcpUserTimeout":10000}}`
	got := sanitizeStreamSettingsForRemote(input)
	if !strings.Contains(got, `"tcpcongestion":"bbr"`) {
		t.Fatalf("node sync must preserve stored tcpcongestion=bbr, got: %s", got)
	}
}
