package ai

import (
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Handing a question over to the Claude and ChatGPT apps (docs/ai.md §6.3,
// §15). Only the current question goes, never the whole chat; a question
// too long for a link is copied to the clipboard instead.
const (
	claudeWeb  = "https://claude.ai/new"
	claudeApp  = "claude://claude.ai/new"
	chatGPTWeb = "https://chatgpt.com/"
	// maxLinkLen bounds a link carrying a question (browsers and the shell
	// handle much longer ones, but some proxies and servers do not).
	maxLinkLen = 8000
	// maxClaudeAppRunes is where the Claude desktop app cuts its q
	// parameter (14336 characters in the version checked); longer questions
	// go through the clipboard so nothing is lost.
	maxClaudeAppRunes = 14000
	// clipboardNoticeDelay lets the window hide before the notification
	// about the clipboard, so it shows as a Windows notification.
	clipboardNoticeDelay = 400 * time.Millisecond
)

// handoffLink returns the link that opens target ("claude" or "chatgpt")
// with question, and whether the question had to go through the clipboard
// instead (the link then opens an empty chat).
func handoffLink(target, question string, claudeAppInstalled bool) (link string, viaClipboard bool) {
	base, q := chatGPTWeb, question
	if target == "claude" {
		base = claudeWeb
		// The app refuses a q starting with "/" (its slash commands); the
		// website takes it.
		if claudeAppInstalled && !strings.HasPrefix(strings.TrimSpace(q), "/") {
			base = claudeApp
			if utf8.RuneCountInString(q) > maxClaudeAppRunes {
				return base, true
			}
		}
	}
	if q == "" {
		return base, false
	}
	link = base + "?q=" + url.QueryEscape(q)
	if len(link) > maxLinkLen {
		return base, true
	}
	return link, false
}

// continueIn opens the Claude or ChatGPT app (or website) with the current
// question: the text typed in the chat, or else its last question.
func (p *Plugin) continueIn(target, instance, typed string) plugin.Result {
	question := strings.TrimSpace(typed)
	if question == "" {
		p.mu.Lock()
		if ch := p.chats[p.views[instance]]; ch != nil {
			question = ch.lastQuestion()
		}
		p.mu.Unlock()
	}
	link, viaClipboard := handoffLink(target, question, target == "claude" && p.o.HasProtocol("claude"))
	if viaClipboard {
		if err := p.h.Clipboard().WriteText(question); err != nil {
			return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: p.h.I18n().T("toast.copyFailed"), Message: err.Error()}}
		}
		l := p.h.I18n()
		app := l.T("app.chatgpt")
		if target == "claude" {
			app = l.T("app.claude")
		}
		toast := plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.questionCopied"), Message: l.T("toast.questionCopiedHint", "app", app)}
		p.h.Go(func() {
			time.Sleep(clipboardNoticeDelay)
			p.h.Toast(toast)
		})
	}
	return plugin.Result{Do: &plugin.ActionDo{Kind: plugin.ActionOpen, Target: link}}
}
