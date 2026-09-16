package ingest

import (
	"encoding/json"
	"regexp"
	"strings"
)

type authKind int

const (
	authNone authKind = iota
	authLoginSuccess
	authLoginFailure
	authLogout
)

// enrich fills what a source implies but its records leave out. It only fills
// fields that are still empty, so a sender's own value always wins.
func (b *builder) enrich() {
	e := &b.ev
	auth := authNone

	switch e.Source {
	case "crowdstrike":
		setText(&e.Vendor, "crowdstrike")
		setText(&e.Product, "falcon")

	case "aws":
		setText(&e.Vendor, "aws")
		setText(&e.Product, "cloudtrail")
		ct := b.cloudTrail()
		setText(&e.EventType, ct.EventName)
		name := deref(e.EventType)
		switch {
		case name == "ConsoleLogin":
			auth = authLoginSuccess
			if ct.ResponseElements.ConsoleLogin == "Failure" {
				auth = authLoginFailure
			}
		case strings.HasPrefix(name, "Create"):
			setText(&e.Action, "create")
		case strings.HasPrefix(name, "Delete"):
			setText(&e.Action, "delete")
		}

	case "m365":
		setText(&e.Vendor, "microsoft")
		setText(&e.Product, "m365")
		switch deref(e.EventType) {
		case "UserLoggedIn":
			auth = authLoginSuccess
		case "UserLoginFailed":
			auth = authLoginFailure
		}
		auth = withOutcome(auth, b.extra("status"))

	case "ad":
		setText(&e.Vendor, "microsoft")
		setText(&e.Product, "windows")
		switch b.extra("event_id") {
		case "4624":
			auth = authLoginSuccess
		case "4625":
			auth = authLoginFailure
		case "4634", "4647":
			auth = authLogout
		}
		setText(&e.EventSubtype, logonTypes[b.extra("logon_type")])

	case "api":
		auth = withOutcome(guessAuth(deref(e.EventType)), b.extra("status"), b.extra("outcome"))

	case "firewall":
		setText(&e.EventType, "traffic")

	case "network":
		setText(&e.EventType, "syslog")
	}

	switch auth {
	case authLoginSuccess:
		setText(&e.Action, "login")
		b.tag(TagAuthSuccess)
	case authLoginFailure:
		setText(&e.Action, "login")
		b.tag(TagAuthFailure)
	case authLogout:
		setText(&e.Action, "logout")
	}
}

// logonTypes names Windows logon types (the LogonType field of 4624/4625).
var logonTypes = map[string]string{
	"2":  "interactive",
	"3":  "network",
	"4":  "batch",
	"5":  "service",
	"7":  "unlock",
	"8":  "network_cleartext",
	"9":  "new_credentials",
	"10": "remote_interactive",
	"11": "cached_interactive",
}

type cloudTrail struct {
	EventName        string `json:"eventName"`
	ResponseElements struct {
		ConsoleLogin string `json:"ConsoleLogin"`
	} `json:"responseElements"`
}

// cloudTrail reads the CloudTrail fields the AWS rules need from raw. A raw
// that is missing or shaped differently yields zero values.
func (b *builder) cloudTrail() cloudTrail {
	var ct cloudTrail
	if raw, ok := b.value("raw"); ok {
		_ = json.Unmarshal(raw, &ct)
	}
	return ct
}

var (
	logoutName  = regexp.MustCompile(`(?i)log_?(out|off)|logged_?out|sign_?out`)
	loginName   = regexp.MustCompile(`(?i)log_?[io]n|logged_?in|sign_?in`)
	failureName = regexp.MustCompile(`(?i)fail`)
)

// guessAuth classifies an application's own event names. It is used only for
// the api source: vendor names such as CloudTrail's CreateLoginProfile would
// match these patterns without being logins.
func guessAuth(eventType string) authKind {
	switch {
	case logoutName.MatchString(eventType):
		return authLogout
	case !loginName.MatchString(eventType):
		return authNone
	case failureName.MatchString(eventType):
		return authLoginFailure
	default:
		return authLoginSuccess
	}
}

// withOutcome lets an explicit status field decide whether a login succeeded.
func withOutcome(auth authKind, statuses ...string) authKind {
	if auth != authLoginSuccess && auth != authLoginFailure {
		return auth
	}
	for _, s := range statuses {
		switch strings.ToLower(s) {
		case "success", "succeeded":
			return authLoginSuccess
		case "failed", "failure":
			return authLoginFailure
		}
	}
	return auth
}

var actionSynonyms = map[string]string{
	"drop": "deny", "dropped": "deny", "block": "deny", "blocked": "deny",
	"reject": "deny", "rejected": "deny", "denied": "deny",
	"accept": "allow", "accepted": "allow", "permit": "allow",
	"permitted": "allow", "pass": "allow", "allowed": "allow",
	"logon": "login", "logoff": "logout",
}

// normalizeAction lowercases the action and folds vendor synonyms into the
// values the requirements list.
func normalizeAction(action *string) {
	if action == nil {
		return
	}
	a := strings.ToLower(*action)
	if s, ok := actionSynonyms[a]; ok {
		a = s
	}
	*action = a
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
