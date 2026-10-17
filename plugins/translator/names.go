package translator

import (
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/translator/engines"
)

// Names chosen at run time, each message spelled out so that glctl i18n
// check sees every key used.

// engineName is an engine's name in the UI language.
func engineName(l plugin.Localizer, id string) string {
	switch id {
	case engGoogle:
		return l.T("engine.google")
	case engMyMemory:
		return l.T("engine.mymemory")
	case engDeepL:
		return l.T("engine.deepl")
	case engAzure:
		return l.T("engine.azure")
	case engYoudao:
		return l.T("engine.youdao")
	case engBaidu:
		return l.T("engine.baidu")
	}
	return id
}

// webName is the name of a translation website.
func webName(l plugin.Localizer, id string) string {
	switch id {
	case "google":
		return l.T("web.google")
	case "deepl":
		return l.T("web.deepl")
	}
	return l.T("web.youdao")
}

// errorText is the message for an error of kind (errorKind) of an engine.
func errorText(l plugin.Localizer, kind, engine string) string {
	switch kind {
	case "quota":
		return l.T("error.quota", "engine", engine)
	case "auth":
		return l.T("error.auth", "engine", engine)
	case "language":
		return l.T("error.language", "engine", engine)
	case "noKey":
		return l.T("error.noKey", "engine", engine)
	case "unavailable":
		return l.T("error.unavailable", "engine", engine)
	case "timeout":
		return l.T("error.timeout", "engine", engine)
	case "network":
		return l.T("error.network", "engine", engine)
	}
	return l.T("error.failed", "engine", engine)
}

// langName is a language's name in the UI language ("Detected" for "").
func langName(l plugin.Localizer, code string) string {
	switch code {
	case "":
		return l.T("lang.auto")
	case engines.English:
		return l.T("lang.en")
	case engines.Simplified:
		return l.T("lang.zh-CN")
	case engines.Traditional:
		return l.T("lang.zh-TW")
	case engines.Japanese:
		return l.T("lang.ja")
	case engines.Korean:
		return l.T("lang.ko")
	case "fr":
		return l.T("lang.fr")
	case "de":
		return l.T("lang.de")
	case "es":
		return l.T("lang.es")
	case engines.Russian:
		return l.T("lang.ru")
	case "pt":
		return l.T("lang.pt")
	case "it":
		return l.T("lang.it")
	case "ar":
		return l.T("lang.ar")
	case "th":
		return l.T("lang.th")
	case "vi":
		return l.T("lang.vi")
	case "id":
		return l.T("lang.id")
	case "nl":
		return l.T("lang.nl")
	case "pl":
		return l.T("lang.pl")
	case "tr":
		return l.T("lang.tr")
	case "uk":
		return l.T("lang.uk")
	case "sv":
		return l.T("lang.sv")
	case "hi":
		return l.T("lang.hi")
	}
	return code
}
