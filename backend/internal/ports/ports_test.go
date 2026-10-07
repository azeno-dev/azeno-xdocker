package ports

import (
	"bufio"
	"strings"
	"testing"
)

func TestParseProcNet(t *testing.T) {
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1388 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0000000000000000 100 0 0 10 0
   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2 1 0000000000000000 100 0 0 10 0
   2: 0100007F:1F91 0100007F:C350 01 00000000:00000000 00:00000000 00000000     0        0 3 1 0000000000000000 100 0 0 10 0`
	ls := ParseProcNet(bufio.NewScanner(strings.NewReader(tcp)), "tcp", "0A")
	if len(ls) != 2 {
		t.Fatalf("listeners = %v", ls)
	}
	if ls[0].Port != 5000 || ls[0].IP != "0.0.0.0" {
		t.Errorf("first = %+v", ls[0])
	}
	if ls[1].Port != 8080 || ls[1].IP != "127.0.0.1" {
		t.Errorf("second = %+v", ls[1])
	}
}

func TestParseReserved(t *testing.T) {
	rs, err := ParseReserved(DefaultReserved + ", 514/udp syslog, 8080")
	if err != nil {
		t.Fatal(err)
	}
	last := rs[len(rs)-1]
	if last.Port != 8080 || last.Proto != "tcp" {
		t.Errorf("last = %+v", last)
	}
	if rs[len(rs)-2].Proto != "udp" || rs[len(rs)-2].Name != "syslog" {
		t.Errorf("udp = %+v", rs[len(rs)-2])
	}
	if _, err := ParseReserved("99999/tcp"); err == nil {
		t.Error("expected error")
	}
}
