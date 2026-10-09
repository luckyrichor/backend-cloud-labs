package room

import "testing"

func TestIPv6QuotaGroupsPrefixAndUnmapsIPv4(t *testing.T) {
	cases := map[string]string{"2001:db8:1:2::1": "2001:db8:1:2::/64", "2001:db8:1:2::abcd": "2001:db8:1:2::/64", "2001:db8:1:3::1": "2001:db8:1:3::/64", "::ffff:192.0.2.1": "192.0.2.1", "192.0.2.1": "192.0.2.1", "fe80::1%eth0": "fe80::/64"}
	for input, want := range cases {
		if got := quotaHost(input); got != want {
			t.Fatalf("%s: %s != %s", input, got, want)
		}
	}
}
