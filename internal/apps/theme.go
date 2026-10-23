package apps

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// Theme customises the page's look. Every field is optional.
type Theme struct {
	// Mode is the default for users who haven't picked one with the theme
	// button: "auto" (follow the OS), "light" or "dark".
	Mode string `yaml:"mode"`
	// TileSize is the minimum tile size in CSS pixels. The page grows tiles to
	// fit the longest single word in any app name.
	TileSize int `yaml:"tileSize"`
	// Accent colours links, the avatar and focus rings.
	Accent string `yaml:"accent"`
	// Background is the page colour. It applies in both modes unless
	// BackgroundDark is also set.
	Background     string `yaml:"background"`
	BackgroundDark string `yaml:"backgroundDark"`
	// BackgroundImage, Logo and Favicon accept the same forms as an app icon.
	// BackgroundImage applies in both modes unless BackgroundImageDark is set.
	BackgroundImage     string `yaml:"backgroundImage"`
	BackgroundImageDark string `yaml:"backgroundImageDark"`
	Logo                string `yaml:"logo"`
	Favicon             string `yaml:"favicon"`
	// CustomCSS is appended to the generated stylesheet.
	CustomCSS string `yaml:"customCSS"`

	// Derived at load time.
	LogoURL    string `yaml:"-"`
	FaviconURL string `yaml:"-"`
	CSS        string `yaml:"-"`
	CSSHash    string `yaml:"-"`
}

const (
	defaultTileSize = 100
	minTileSize     = 56
	maxTileSize     = 240
)

// cssValue allows colours like #abc, rebeccapurple or rgb(1 2 3 / 50%), but
// nothing that could end the declaration it is placed in.
var cssValue = regexp.MustCompile(`^[#a-zA-Z0-9(),.%\s/+-]+$`)

func (t *Theme) prepare() error {
	switch t.Mode {
	case "":
		t.Mode = "auto"
	case "auto", "light", "dark":
	default:
		return fmt.Errorf("mode %q must be auto, light or dark", t.Mode)
	}
	switch {
	case t.TileSize == 0:
		t.TileSize = defaultTileSize
	case t.TileSize < minTileSize || t.TileSize > maxTileSize:
		return fmt.Errorf("tileSize must be between %d and %d", minTileSize, maxTileSize)
	}
	for name, v := range map[string]string{"accent": t.Accent, "background": t.Background, "backgroundDark": t.BackgroundDark} {
		if v != "" && !cssValue.MatchString(v) {
			return fmt.Errorf("%s %q is not a valid CSS colour", name, v)
		}
	}
	bgImage, bgImageDark := resolveIcon(t.BackgroundImage), resolveIcon(t.BackgroundImageDark)
	for name, v := range map[string]string{"backgroundImage": bgImage, "backgroundImageDark": bgImageDark} {
		if strings.ContainsAny(v, "\"\\\n\r") {
			return fmt.Errorf("%s %q contains characters not allowed in a URL", name, v)
		}
	}
	t.LogoURL = resolveIcon(t.Logo)
	t.FaviconURL = resolveIcon(t.Favicon)

	// The page's stylesheet reads these --theme-* variables with fallbacks, so
	// only what the admin set is emitted here.
	var b strings.Builder
	b.WriteString(":root {\n")
	fmt.Fprintf(&b, "  --theme-tile: %dpx;\n", t.TileSize)
	if t.Accent != "" {
		fmt.Fprintf(&b, "  --theme-accent: %s;\n", t.Accent)
	}
	if t.Background != "" {
		fmt.Fprintf(&b, "  --theme-bg: %s;\n", t.Background)
	}
	if t.BackgroundDark != "" {
		fmt.Fprintf(&b, "  --theme-bg-dark: %s;\n", t.BackgroundDark)
	}
	if bgImage != "" {
		fmt.Fprintf(&b, "  --theme-bg-image: url(\"%s\");\n", bgImage)
	}
	if bgImageDark != "" {
		fmt.Fprintf(&b, "  --theme-bg-image-dark: url(\"%s\");\n", bgImageDark)
	}
	b.WriteString("}\n")
	if t.CustomCSS != "" {
		b.WriteString("\n/* customCSS */\n")
		b.WriteString(t.CustomCSS)
		b.WriteString("\n")
	}
	t.CSS = b.String()
	sum := sha256.Sum256([]byte(t.CSS))
	t.CSSHash = hex.EncodeToString(sum[:6])
	return nil
}
