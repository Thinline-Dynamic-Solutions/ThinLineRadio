package main

import "testing"

func TestCategorizeLogMessage(t *testing.T) {
	cases := []struct {
		message string
		want    string
	}{
		{"push notification: sent to 3 devices", LogCategoryPush},
		{"Stripe webhook received: checkout.session.completed", LogCategoryBilling},
		{"Email sent successfully via SendGrid to user@example.com", LogCategoryEmail},
		{"Central Management: Created user foo@bar.com", LogCategoryCentral},
		{"dirwatch.ingest: file not found, /tmp/x", LogCategoryDirwatch},
		{"tone detection starting for call 42", LogCategoryTones},
		{"pre-alert: user 1 gets alert", LogCategoryAlerts},
		{"admin: Invalid login attempt | IP=1.2.3.4", LogCategoryAuth},
		{"Hydra retrieval: stored transcript for call 1", LogCategoryTranscription},
		{"new listener from ip 10.0.0.1", LogCategoryWebsocket},
		{"tone auto-learn: candidate abc on talkgroup 1", LogCategoryAutoLearn},
		{"api: Invalid PIN", LogCategoryAuth},
		{"api: unauthorized", LogCategoryAuth},
		{"api: Invalid credentials | IP=1.2.3.4 | Endpoint=POST /api/user/login | UserAgent=Test", LogCategoryAuth},
		{"api: Public registration group not found", LogCategoryUsers},
		{"api: [UPLOAD PARSED] -> Valid, passing to HandleCall", LogCategoryCalls},
		{"api: Incomplete call data: missing audio | SystemId=1 TalkgroupId=2 AudioLen=0", LogCategoryCalls},
		{"downstream: system=1 talkgroup=2 file=x to http://x success", LogCategoryDownstream},
		{"no-audio check OK: system 'Fire' within threshold", LogCategoryHealth},
		{"options changed", LogCategoryAdmin},
		{"something completely random xyz", LogCategoryUncategorized},
	}

	for _, tc := range cases {
		got := CategorizeLogMessage(tc.message)
		if got != tc.want {
			t.Errorf("CategorizeLogMessage(%q) = %q, want %q", tc.message, got, tc.want)
		}
	}
}
