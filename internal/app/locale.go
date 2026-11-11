package app

import (
	"os"
	"strings"
	"sync/atomic"

	_ "github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"

	"mygopia/internal/config"
)

// activeLangAtom stores the resolved display language thread-safely across UI, headless tests, & background tasks.
var activeLangAtom atomic.Value

func init() {
	activeLangAtom.Store(config.LangEN)
	i18n.SetLocaleFunc(currentLanguage)
}

func currentLanguage() string {
	if v, ok := activeLangAtom.Load().(string); ok && v != "" {
		return v
	}
	return config.LangEN
}

func matchLocaleTag(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	lower = strings.ReplaceAll(lower, "_", "-")
	if i := strings.IndexByte(lower, '.'); i >= 0 {
		lower = lower[:i]
	}
	if i := strings.IndexByte(lower, '@'); i >= 0 {
		lower = lower[:i]
	}
	switch {
	case lower == "":
		return ""
	case strings.HasPrefix(lower, "zh-hant"), strings.HasPrefix(lower, "zh-tw"), strings.HasPrefix(lower, "zh-hk"), strings.HasPrefix(lower, "zh-mo"):
		return config.LangZhHant
	case strings.HasPrefix(lower, "zh"):
		return config.LangZhHans
	case strings.HasPrefix(lower, "ja"):
		return config.LangJA
	case strings.HasPrefix(lower, "fr"):
		return config.LangFR
	case strings.HasPrefix(lower, "de"):
		return config.LangDE
	case strings.HasPrefix(lower, "es"):
		return config.LangES
	case strings.HasPrefix(lower, "en"):
		return config.LangEN
	default:
		return ""
	}
}

// ResolveLanguage resolves config.LangSystem or an explicit language code against preferred OS locales.
func ResolveLanguage(mode string, preferred []string) string {
	switch mode {
	case config.LangEN, config.LangZhHans, config.LangZhHant, config.LangJA, config.LangFR, config.LangDE, config.LangES:
		return mode
	default:
		for _, tag := range preferred {
			if m := matchLocaleTag(tag); m != "" {
				return m
			}
		}
		for _, envKey := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
			if m := matchLocaleTag(os.Getenv(envKey)); m != "" {
				return m
			}
		}
		return config.LangEN
	}
}

func setCurrentLanguage(lang string) {
	switch lang {
	case config.LangEN, config.LangZhHans, config.LangZhHant, config.LangJA, config.LangFR, config.LangDE, config.LangES:
		activeLangAtom.Store(lang)
	default:
		activeLangAtom.Store(config.LangEN)
	}
}
