package analytics

import (
	"regexp"
	"strings"
)

var botRe = regexp.MustCompile(`(?i)bot|crawl|spider|slurp|scrape|headless|lighthouse|pingdom|uptime|monitor|preview|` +
	`facebookexternalhit|curl|wget|python|go-http|java/|okhttp|axios|node-fetch|httpclient|libwww|^$`)

// isBot is true for crawlers, link previews, monitors and scripts.
func isBot(ua string) bool { return botRe.MatchString(ua) }

// first returns the name of the first rule whose token ua contains: order
// matters, since Chrome's UA also says Safari and Edge's also says Chrome.
func first(ua string, rules [][2]string) string {
	for _, r := range rules {
		if strings.Contains(ua, r[0]) {
			return r[1]
		}
	}
	return "Other"
}

var browsers = [][2]string{
	{"Edg/", "Edge"}, {"EdgA/", "Edge"}, {"EdgiOS/", "Edge"}, {"OPR/", "Opera"}, {"Opera", "Opera"},
	{"SamsungBrowser/", "Samsung Internet"}, {"YaBrowser/", "Yandex"}, {"Vivaldi/", "Vivaldi"},
	{"Firefox/", "Firefox"}, {"FxiOS/", "Firefox"}, {"CriOS/", "Chrome"}, {"Chrome/", "Chrome"},
	{"Safari/", "Safari"},
}

var systems = [][2]string{
	{"Windows", "Windows"}, {"iPhone", "iOS"}, {"iPad", "iOS"}, {"iPod", "iOS"}, {"Android", "Android"},
	{"CrOS", "ChromeOS"}, {"Mac OS X", "macOS"}, {"Macintosh", "macOS"}, {"Linux", "Linux"},
}

// device is mobile, tablet or desktop: the UA when it says so, else the
// screen width, as Umami does.
func device(ua string, screen int) string {
	switch {
	case strings.Contains(ua, "iPad") || strings.Contains(ua, "Tablet") ||
		strings.Contains(ua, "Android") && !strings.Contains(ua, "Mobile"):
		return "tablet"
	case strings.Contains(ua, "Mobi") || strings.Contains(ua, "iPhone"):
		return "mobile"
	case screen > 0 && screen < 768:
		return "mobile"
	case screen > 0 && screen < 1024:
		return "tablet"
	}
	return "desktop"
}
