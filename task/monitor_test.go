package task

import "testing"

func Test_processIP(t *testing.T) {
	t.Log(processIP("read tcp 192.168.11.244:49686->10.78.111.103:443: i/o timeout"))
}
