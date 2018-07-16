package ip

import (
	"net"
	"testing"
)

func TestIPBetween(t *testing.T) {
	HandleIPBetween(t, "0.0.0.0", "255.255.255.255", "128.128.128.128", true)
	HandleIPBetween(t, "0.0.0.0", "128.128.128.128", "255.255.255.255", false)
	HandleIPBetween(t, "74.50.153.0", "74.50.153.4", "74.50.153.0", true)
	HandleIPBetween(t, "74.50.153.0", "74.50.153.4", "74.50.153.4", true)
	HandleIPBetween(t, "74.50.153.0", "74.50.153.4", "74.50.153.5", false)
	HandleIPBetween(t, "2001:0db8:85a3:0000:0000:8a2e:0370:7334", "74.50.153.4", "74.50.153.2", false)
	HandleIPBetween(t, "2001:0db8:85a3:0000:0000:8a2e:0370:7334", "2001:0db8:85a3:0000:0000:8a2e:0370:8334", "2001:0db8:85a3:0000:0000:8a2e:0370:7334", true)
	HandleIPBetween(t, "2001:0db8:85a3:0000:0000:8a2e:0370:7334", "2001:0db8:85a3:0000:0000:8a2e:0370:8334", "2001:0db8:85a3:0000:0000:8a2e:0370:7350", true)
	HandleIPBetween(t, "2001:0db8:85a3:0000:0000:8a2e:0370:7334", "2001:0db8:85a3:0000:0000:8a2e:0370:8334", "2001:0db8:85a3:0000:0000:8a2e:0370:8334", true)
	HandleIPBetween(t, "2001:0db8:85a3:0000:0000:8a2e:0370:7334", "2001:0db8:85a3:0000:0000:8a2e:0370:8334", "2001:0db8:85a3:0000:0000:8a2e:0370:8335", false)
	HandleIPBetween(t, "::ffff:192.0.2.128", "::ffff:192.0.2.250", "::ffff:192.0.2.127", false)
	HandleIPBetween(t, "::ffff:192.0.2.128", "::ffff:192.0.2.250", "::ffff:192.0.2.128", true)
	HandleIPBetween(t, "::ffff:192.0.2.128", "::ffff:192.0.2.250", "::ffff:192.0.2.129", true)
	HandleIPBetween(t, "::ffff:192.0.2.128", "::ffff:192.0.2.250", "::ffff:192.0.2.250", true)
	HandleIPBetween(t, "::ffff:192.0.2.128", "::ffff:192.0.2.250", "::ffff:192.0.2.251", false)
	HandleIPBetween(t, "::ffff:192.0.2.128", "::ffff:192.0.2.250", "192.0.2.130", true)
	HandleIPBetween(t, "192.0.2.128", "192.0.2.250", "::ffff:192.0.2.130", true)
	HandleIPBetween(t, "idonotparse", "192.0.2.250", "::ffff:192.0.2.130", false)

}

func HandleIPBetween(t *testing.T, from string, to string, test string, assert bool) {
	res := Between(net.ParseIP(from), net.ParseIP(to), net.ParseIP(test))
	if res != assert {
		t.Errorf("Assertion (have: %t should be: %t) failed on range %s-%s with test %s", res, assert, from, to, test)
	}
}

func TestParseIPRangeString(t *testing.T) {
	HandleParseIPRangeString(t, "127.0.0.1", "127.0.0.1", "127.0.0.1", true)
	HandleParseIPRangeString(t, "127.0.0.1/24", "127.0.0.0", "127.0.0.255", true)
	HandleParseIPRangeString(t, "127.0.0.1/29", "127.0.0.0", "127.0.0.7", true)
	HandleParseIPRangeString(t, "127.0.0.1/24-127.0.0.1", "127.0.0.1", "127.0.0.1", false)
	HandleParseIPRangeString(t, "127.0.0.1/", "127.0.0.1", "127.0.0.1", false)
	HandleParseIPRangeString(t, "127.0.0.1/bad", "127.0.0.1", "127.0.0.1", false)
	HandleParseIPRangeString(t, " 127.0.0.1", "127.0.0.1", "127.0.0.1", true)
	HandleParseIPRangeString(t, "127.0.0.1-127.0.0.2", "127.0.0.1", "127.0.0.2", true)
	HandleParseIPRangeString(t, "127.0.0.1-127.0.1.0", "127.0.0.1", "127.0.1.0", true)
	HandleParseIPRangeString(t, " 127.0.0.1 - 127.0.1.0 ", "127.0.0.1", "127.0.1.0", true)
	HandleParseIPRangeString(t, "127.1.1.0-127.0.1.0", "127.1.1.0", "127.1.0.0", false)

}

func HandleParseIPRangeString(t *testing.T, rangeIP string, from string, to string, ok bool) {
	fromIP, toIP, err := ParseIPRangeString(rangeIP)
	if ok && err != nil {
		t.Errorf("Assertion: Expected OK for range: %s, got error: %s", rangeIP, err)
	}
	if !ok && err == nil {
		t.Errorf("Assertion: Expected error for range: %s, got OK range: %s-%s", rangeIP, fromIP.String(), toIP.String())
	}

	if err == nil {
		if from != fromIP.String() || to != toIP.String() {
			t.Errorf("Assertion: expected range %s, got range %s-%s", rangeIP, fromIP.String(), toIP.String())
		}

	}
}
