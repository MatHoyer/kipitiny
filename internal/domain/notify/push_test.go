package notify

import "testing"

func TestValidPushEndpoint(t *testing.T) {
	for ep, want := range map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":                true,
		"https://updates.push.services.mozilla.com/wpush/v2/abc": true,
		"https://web.push.apple.com/QGx":                         true,
		"https://wns2-par02p.notify.windows.com/w/?token=abc":    true,
		"http://fcm.googleapis.com/fcm/send/abc":                 false,
		"https://fcm.googleapis.com:8443/fcm/send/abc":           false,
		"https://evil.com/fcm.googleapis.com":                    false,
		"https://push.apple.com.evil.com/x":                      false,
		"https://evilpush.apple.com/x":                           false,
		"https://user@fcm.googleapis.com/x":                      false,
		"https://169.254.169.254/latest/meta-data":               false,
	} {
		if ValidPushEndpoint(ep) != want {
			t.Errorf("%s: want %v", ep, want)
		}
	}
}
