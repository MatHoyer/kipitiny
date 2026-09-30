package core

import "testing"

func TestCronParser(t *testing.T) {
	for expr, ok := range map[string]bool{
		"0 3 * * *":                      true,
		"@daily":                         true,
		"@every 6h":                      true,
		"CRON_TZ=Europe/Paris 0 3 * * *": true,
		"every day":                      false,
		"0 3 * *":                        false,
		"* * * * * *":                    false, // no seconds field
	} {
		_, err := cronParser.Parse(expr)
		if (err == nil) != ok {
			t.Errorf("Parse(%q) err = %v, want ok=%v", expr, err, ok)
		}
	}
}
