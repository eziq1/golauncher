// Package protocol defines the messages of the external plugin protocol:
// JSON-RPC 2.0 over the plugin process's stdin/stdout, one JSON object per
// line. See docs/plugin-protocol.md for the specification.
package protocol

import (
	"encoding/json/jsontext"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Version is the protocol version spoken by this host and SDK. A plugin
// whose initialize result names a different version is not loaded.
const Version = 1

// Methods the host calls on the plugin (requests).
const (
	MethodInitialize        = "initialize"
	MethodRootItems         = "rootItems"
	MethodView              = "view"
	MethodHandleAction      = "handleAction"
	MethodPreferenceOptions = "preferenceOptions"
	MethodContextItems      = "contextItems"
	MethodShutdown          = "shutdown"
)

// Notifications the host sends to the plugin.
const (
	NotifyLocaleChanged      = "localeChanged"
	NotifyPreferencesChanged = "preferencesChanged"
	NotifyEvent              = "event"
	NotifyViewClosed         = "viewClosed"
	// NotifyTaskFinished tells the plugin that a task it started with
	// host/task.runProcess ended.
	NotifyTaskFinished = "taskFinished"
)

// Methods the plugin calls on the host (requests).
const (
	HostStorageGet          = "host/storage.get"
	HostStorageSet          = "host/storage.set"
	HostStorageDelete       = "host/storage.delete"
	HostClipboardRead       = "host/clipboard.read"
	HostClipboardWriteText  = "host/clipboard.writeText"
	HostClipboardWriteImage = "host/clipboard.writeImage"
	HostClipboardWriteFiles = "host/clipboard.writeFiles"
	HostSubscribe           = "host/subscribe"
	// HostLog may be sent as a request or as a notification.
	HostLog = "host/log"
	// HostTaskRunProcess queues a background task (needs the exec
	// permission).
	HostTaskRunProcess = "host/task.runProcess"
	// HostToast shows a toast or notification; it may be sent as a request
	// or as a notification.
	HostToast = "host/toast"
)

// Notifications the plugin sends to the host.
const (
	NotifyInvalidate = "host/invalidate"
	NotifyRender     = "ui/render"
)

// InitializeParams are the parameters of initialize.
type InitializeParams struct {
	Protocol    int    `json:"protocol"`
	HostVersion string `json:"hostVersion"`
	PluginID    string `json:"pluginId"`
	Locale      string `json:"locale"`
	DataDir     string `json:"dataDir"`
	CacheDir    string `json:"cacheDir"`
	// Preferences holds the effective value of every declared preference
	// (user value or default).
	Preferences map[string]jsontext.Value `json:"preferences"`
}

// InitializeResult is the result of initialize.
type InitializeResult struct {
	Protocol     int          `json:"protocol"`
	Capabilities Capabilities `json:"capabilities"`
}

// Capabilities tell the host which methods the plugin implements. The host
// does not call methods whose capability is false.
type Capabilities struct {
	RootItems         bool `json:"rootItems,omitzero"`
	Views             bool `json:"views,omitzero"`
	Actions           bool `json:"actions,omitzero"`
	PreferenceOptions bool `json:"preferenceOptions,omitzero"`
	Render            bool `json:"render,omitzero"`
	ContextItems      bool `json:"contextItems,omitzero"`
}

// RootItemsParams are the parameters of rootItems; the result is []plugin.Item.
type RootItemsParams struct {
	Query string `json:"query"`
	// Raw is the query as typed, untrimmed (plugin.Query.Raw).
	Raw string `json:"raw,omitempty"`
	// Context is the context of the current show, if any.
	Context *plugin.Context `json:"context,omitempty"`
}

// ContextItemsParams are the parameters of contextItems; the result is
// []plugin.Item.
type ContextItemsParams struct {
	Context plugin.Context `json:"context"`
}

// ViewParams are the parameters of view; the result is a plugin.View.
type ViewParams struct {
	Request plugin.ViewRequest `json:"request"`
}

// HandleActionParams are the parameters of handleAction; the result is a
// plugin.Result.
type HandleActionParams struct {
	Action plugin.ActionDo `json:"action"`
}

// PreferenceOptionsParams are the parameters of preferenceOptions; the
// result is []plugin.Option.
type PreferenceOptionsParams struct {
	Name string `json:"name"`
}

// LocaleChangedParams are the parameters of localeChanged.
type LocaleChangedParams struct {
	Locale string `json:"locale"`
}

// PreferencesChangedParams are the parameters of preferencesChanged.
type PreferencesChangedParams struct {
	Preferences map[string]jsontext.Value `json:"preferences"`
}

// EventParams are the parameters of event (a host event the plugin
// subscribed to with host/subscribe).
type EventParams struct {
	Name string `json:"name"`
}

// ViewClosedParams are the parameters of viewClosed.
type ViewClosedParams struct {
	Instance string `json:"instance"`
}

// StorageKeyParams are the parameters of host/storage.get and .delete.
type StorageKeyParams struct {
	Key string `json:"key"`
}

// StorageValue is a stored value. Text values use Value; values that are
// not valid UTF-8 use Base64.
type StorageValue struct {
	Value  string `json:"value,omitempty"`
	Base64 string `json:"base64,omitempty"`
}

// StorageGetResult is the result of host/storage.get.
type StorageGetResult struct {
	Found bool `json:"found"`
	StorageValue
}

// StorageSetParams are the parameters of host/storage.set.
type StorageSetParams struct {
	Key string `json:"key"`
	StorageValue
}

// ClipboardContent is the result of host/clipboard.read.
type ClipboardContent struct {
	Text      string   `json:"text,omitempty"`
	Image     string   `json:"image,omitempty"` // base64 PNG
	Files     []string `json:"files,omitempty"`
	SourceApp string   `json:"sourceApp,omitempty"`
	Exclude   bool     `json:"exclude,omitzero"`
}

// WriteTextParams are the parameters of host/clipboard.writeText.
type WriteTextParams struct {
	Text string `json:"text"`
	// Secret asks clipboard history not to record the text (Clipboard.WriteSecret).
	Secret bool `json:"secret,omitempty"`
}

// WriteImageParams are the parameters of host/clipboard.writeImage.
type WriteImageParams struct {
	PNG string `json:"png"` // base64
}

// WriteFilesParams are the parameters of host/clipboard.writeFiles.
type WriteFilesParams struct {
	Paths []string `json:"paths"`
}

// SubscribeParams are the parameters of host/subscribe.
type SubscribeParams struct {
	Event string `json:"event"`
}

// LogParams are the parameters of host/log. Level is debug, info, warn or
// error.
type LogParams struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// RunProcessParams are the parameters of host/task.runProcess.
// Spec.OnDone is not sent: the plugin learns the outcome from the
// taskFinished notification.
type RunProcessParams struct {
	Spec plugin.TaskSpec    `json:"spec"`
	Cmd  plugin.ProcessSpec `json:"cmd"`
}

// RunProcessResult is the result of host/task.runProcess.
type RunProcessResult struct {
	ID string `json:"id"`
}

// TaskFinishedParams are the parameters of taskFinished.
type TaskFinishedParams struct {
	Result plugin.TaskResult `json:"result"`
}

// ToastParams are the parameters of host/toast.
type ToastParams struct {
	Toast plugin.Toast `json:"toast"`
}

// InvalidateParams are the parameters of host/invalidate.
type InvalidateParams struct {
	ViewID string `json:"viewId"`
}

// RenderParams are the parameters of ui/render. The host drops a render
// whose Version is not greater than the last one accepted for Instance.
type RenderParams struct {
	Instance string      `json:"instance"`
	Version  int64       `json:"version"`
	View     plugin.View `json:"view"`
}
