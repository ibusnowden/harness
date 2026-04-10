package vulnerable

import "testing"

func TestInsecureTLSConfigRejectsDisabledVerification(t *testing.T) {
	if InsecureTLSConfig().InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify must remain false")
	}
}
