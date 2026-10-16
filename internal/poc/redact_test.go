package poc

import "testing"

func TestSafeURLDropsQueryAndFragment(t *testing.T) {
	got, status := SafeURL("https://img.example/a.jpg?token=secret&x=1#part")
	if got == nil || *got != "https://img.example/a.jpg" || status != FieldPresent {
		t.Fatalf("got=%v status=%s", got, status)
	}
}

func TestSafeURLRejectsCredentialsAndNonHTTP(t *testing.T) {
	for _, raw := range []string{
		"https://user:secret@example.test/a",
		"file:///tmp/a",
		"javascript:alert(1)",
	} {
		if got, status := SafeURL(raw); got != nil || status != FieldRedactedForSafety {
			t.Fatalf("SafeURL(%q)=(%v,%s)", raw, got, status)
		}
	}
}

func TestSafeProfileURLKeepsOnlyRealChannelsProfileURLs(t *testing.T) {
	generated := "https://channels.weixin.qq.com/web/pages/profile?finderUsername=v2_author%40finder"
	got, status := SafeProfileURL(generated)
	if got == nil || *got != generated || status != FieldPresent {
		t.Fatalf("got=%v status=%s", got, status)
	}
	for _, raw := range []string{
		"https://channels.weixin.qq.com/web/pages/profile",
		"http://channels.weixin.qq.com/web/pages/profile?username=author",
		"https://channels.weixin.qq.com/web/pages/home?finderUsername=v2_author@finder",
		"https://channels.weixin.qq.com/web/pages/profile?token=secret",
		"https://example.test/web/pages/profile?finderUsername=author",
	} {
		if got, status := SafeProfileURL(raw); got != nil || status != FieldRedactedForSafety {
			t.Fatalf("SafeProfileURL(%q)=(%v,%s)", raw, got, status)
		}
	}
}

func TestScanOutputRejectsCredentials(t *testing.T) {
	cases := [][]byte{
		[]byte(`{"authorization":"Bearer abc"}`),
		[]byte(`{"text":"-----BEGIN ` + `PRIVATE KEY-----"}`),
		[]byte(`{"avatar_url":"https://x/a?session=s"}`),
		[]byte(`{"cookie":"secret"}`),
		[]byte(`{"token":"secret"}`),
		[]byte(`{"session_token":"secret"}`),
		[]byte(`{"private_key":"secret"}`),
		[]byte("Set-Cookie: sid=secret"),
	}
	for _, raw := range cases {
		if err := ScanOrdinaryOutput(raw); err == nil {
			t.Fatalf("accepted secret: %q", raw)
		}
	}
	if err := ScanOrdinaryOutput([]byte(`{"profile_url":"https://channels.weixin.qq.com/web/pages/profile?finderUsername=author"}`)); err != nil {
		t.Fatalf("approved profile URL rejected: %v", err)
	}
}

func TestRedactStringPreservesOrdinaryComment(t *testing.T) {
	want := "装修进度很满意"
	got, status := RedactString(want)
	if got == nil || *got != want || status != FieldPresent {
		t.Fatalf("got=%v status=%s", got, status)
	}

	if got, status = RedactString("Bearer secret"); got != nil || status != FieldRedactedForSafety {
		t.Fatalf("secret got=%v status=%s", got, status)
	}
}
