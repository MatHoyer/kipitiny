package ingest

import "testing"

const chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

func TestUA(t *testing.T) {
	for _, c := range []struct{ ua, browser, os, device string }{
		{chromeUA, "Chrome", "Windows", "desktop"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 Version/17.0 Safari/605.1.15", "Safari", "macOS", "desktop"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0", "Firefox", "Linux", "desktop"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/130.0 Mobile Safari/537.36 EdgA/130.0", "Edge", "Android", "mobile"},
		{"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Safari/604.1", "Safari", "iOS", "tablet"},
	} {
		if b, o, d := first(c.ua, browsers), first(c.ua, systems), device(c.ua, 0); b != c.browser || o != c.os || d != c.device {
			t.Errorf("%s: %s %s %s", c.ua, b, o, d)
		}
	}
	if !isBot("curl/8.0") || !isBot("") || isBot(chromeUA) {
		t.Error("bot detection")
	}
}
