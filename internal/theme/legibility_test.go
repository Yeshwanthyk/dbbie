package theme

import (
	"fmt"
	"math"
	"testing"
)

func luminance(t *testing.T, color string) float64 {
	t.Helper()
	var r, g, b uint8
	if _, err := fmt.Sscanf(color, "#%02x%02x%02x", &r, &g, &b); err != nil {
		t.Fatal(err)
	}
	linear := func(c uint8) float64 {
		v := float64(c) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

func TestThemeTextAndModePillsRemainLegible(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			theme, _ := Resolve(name)
			check := func(fg, bg string) {
				a, b := luminance(t, fg), luminance(t, bg)
				ratio := (max(a, b) + 0.05) / (min(a, b) + 0.05)
				if ratio < 4.5 {
					t.Errorf("%s on %s contrast %.2f, want >=4.5", fg, bg, ratio)
				}
			}
			for _, fg := range []string{theme.Text, theme.Muted, theme.Primary, theme.Success, theme.Danger, theme.Agent, theme.Accent} {
				check(fg, theme.Panel)
				check(fg, theme.Selection)
			}
			for _, bg := range []string{theme.Primary, theme.Success, theme.Agent, theme.Accent} {
				check(theme.Background, bg)
			}
		})
	}
}
