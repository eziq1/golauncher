package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Locale is the text of one locale manifest.
type Locale struct {
	Tag              string // en-US, zh-CN
	ShortDescription string
	Description      string
	Tags             []string
}

// Locales are the package's locales; the first is the default one.
var Locales = []Locale{
	{
		Tag:              "en-US",
		ShortDescription: "A Raycast-style launcher for Windows and macOS, written in Go.",
		Description: "Press Alt+Space anywhere to search apps, commands, files and math. " +
			"Everything is a plugin: a calculator with unit and currency conversion, clipboard history, " +
			"file search with Everything, Scoop and WinGet packages, system commands, world clock, timers, " +
			"quicklinks, AI chat and more, plus third-party plugins. " +
			"Actions for the current File Explorer folder and for the selected text. " +
			"glctl, a headless CLI, drives the same engine.",
		Tags: []string{"launcher", "raycast", "spotlight", "productivity", "search", "clipboard-manager", "calculator", "plugins"},
	},
	{
		Tag:              "zh-CN",
		ShortDescription: "类 Raycast 的 Windows 和 macOS 启动器，用 Go 编写。",
		Description: "在任何地方按 Alt+Space 搜索应用、命令、文件和算式。" +
			"所有功能都是插件：带单位和汇率换算的计算器、剪贴板历史、用 Everything 搜索文件、Scoop 和 WinGet 软件包、" +
			"系统命令、世界时钟、计时器、快速链接、AI 对话等，还能安装第三方插件。" +
			"针对资源管理器当前目录和选中文字的操作。无头命令行 glctl 驱动同一个引擎。",
		Tags: []string{"启动器", "效率", "搜索", "剪贴板", "计算器", "插件", "launcher", "raycast"},
	},
}

const (
	header = "# Created with scripts/release/wingetmanifest (github.com/eziq1/golauncher)\n" +
		"# yaml-language-server: $schema=https://aka.ms/winget-manifest.%s.%s.schema.json\n\n"
	homepage = "https://github.com/" + Repo
)

// Manifests returns the manifest files of rel by file name.
func Manifests(rel Release) map[string]string {
	files := map[string]string{
		PackageID + ".yaml":           versionManifest(rel),
		PackageID + ".installer.yaml": installerManifest(rel),
	}
	for i, l := range Locales {
		files[PackageID+".locale."+l.Tag+".yaml"] = localeManifest(rel, l, i == 0)
	}
	return files
}

type yamlWriter struct{ strings.Builder }

func (w *yamlWriter) kv(indent, key, value string) {
	fmt.Fprintf(w, "%s%s: %s\n", indent, key, quote(value))
}

func (w *yamlWriter) end(typ string) string {
	w.kv("", "ManifestType", typ)
	w.kv("", "ManifestVersion", ManifestVersion)
	return w.String()
}

func start(schema string, rel Release) *yamlWriter {
	w := &yamlWriter{}
	fmt.Fprintf(w, header, schema, ManifestVersion)
	w.kv("", "PackageIdentifier", PackageID)
	w.kv("", "PackageVersion", rel.Version)
	return w
}

func versionManifest(rel Release) string {
	w := start("version", rel)
	w.kv("", "DefaultLocale", Locales[0].Tag)
	return w.end("version")
}

func installerManifest(rel Release) string {
	w := start("installer", rel)
	w.kv("", "MinimumOSVersion", MinimumOSVersion)
	w.kv("", "InstallerType", "zip")
	w.kv("", "NestedInstallerType", "portable")
	w.WriteString("NestedInstallerFiles:\n")
	for _, f := range []struct{ file, alias string }{{"golauncher.exe", "golauncher"}, {"glctl.exe", "glctl"}} {
		w.kv("- ", "RelativeFilePath", f.file)
		w.kv("  ", "PortableCommandAlias", f.alias)
	}
	w.WriteString("Commands:\n- golauncher\n- glctl\n")
	w.kv("", "ReleaseDate", rel.Date)
	w.WriteString("Installers:\n")
	for _, in := range rel.Installers {
		w.kv("- ", "Architecture", in.Arch)
		w.kv("  ", "InstallerUrl", in.URL)
		w.kv("  ", "InstallerSha256", strings.ToUpper(in.SHA256))
	}
	return w.end("installer")
}

func localeManifest(rel Release, l Locale, isDefault bool) string {
	schema, typ := "locale", "locale"
	if isDefault {
		schema, typ = "defaultLocale", "defaultLocale"
	}
	w := start(schema, rel)
	w.kv("", "PackageLocale", l.Tag)
	w.kv("", "Publisher", "eziq1")
	w.kv("", "PublisherUrl", "https://github.com/eziq1")
	w.kv("", "PublisherSupportUrl", homepage+"/issues")
	w.kv("", "PackageName", "golauncher")
	w.kv("", "PackageUrl", homepage)
	w.kv("", "License", "MIT")
	w.kv("", "LicenseUrl", homepage+"/blob/main/LICENSE")
	w.kv("", "Copyright", "Copyright (c) 2026 eziq1")
	w.kv("", "ShortDescription", l.ShortDescription)
	w.kv("", "Description", l.Description)
	if isDefault {
		w.kv("", "Moniker", "golauncher")
	}
	w.WriteString("Tags:\n")
	for _, t := range l.Tags {
		w.WriteString("- " + quote(t) + "\n")
	}
	w.kv("", "ReleaseNotesUrl", fmt.Sprintf("%s/releases/tag/v%s", homepage, rel.Version))
	readme := homepage + "#readme"
	if l.Tag == "zh-CN" {
		readme = homepage + "/blob/main/README.zh-CN.md"
	}
	w.WriteString("Documentations:\n")
	w.kv("- ", "DocumentLabel", "README")
	w.kv("  ", "DocumentUrl", readme)
	return w.end(typ)
}

// quote returns s as a YAML scalar: plain when that reads back as the same
// string, else single-quoted.
func quote(s string) string {
	if plain(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func plain(s string) bool {
	if s == "" || strings.TrimSpace(s) != s || strings.ContainsAny(s[:1], "-?:,[]{}#&*!|>'\"%@`") ||
		strings.Contains(s, ": ") || strings.Contains(s, " #") || strings.HasSuffix(s, ":") ||
		strings.ContainsAny(s, "\n\t") {
		return false
	}
	switch strings.ToLower(s) {
	case "true", "false", "yes", "no", "on", "off", "null", "~", "y", "n":
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err != nil
}
