package poc

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCollectCommentsKeepsMediaCandidatesOnlyForAcceptedComments(t *testing.T) {
	item := map[string]any{
		"commentId": "duplicate-media",
		"contentInfo": map[string]any{"emoticonInfos": []any{
			map[string]any{"Url": "https://media.example.test/sticker.png"},
		}},
	}
	api := &fixturePageAPI{responses: [][]byte{
		commentPage(t, []map[string]any{item}, "next"),
		commentPage(t, []map[string]any{item}, ""),
	}}
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "media-candidate-dedup"), &fixtureClock{})
	comments, _, err := collector.CollectComments(
		context.Background(),
		approvedTestOptions(),
		fixtureWork("work-1", "nonce-1", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("comments=%d", len(comments))
	}
	candidates := collector.drainCommentMediaCandidates()
	if len(candidates) != 1 || candidates[0].commentRef != comments[0].Content.MediaRef {
		t.Fatalf("candidates=%+v comment=%+v", candidates, comments[0])
	}
	if drained := collector.drainCommentMediaCandidates(); len(drained) != 0 {
		t.Fatalf("second drain=%+v", drained)
	}
}

func TestMapCommentContentInfoMediaPrecedence(t *testing.T) {
	tests := []struct {
		name           string
		item           map[string]any
		wantTypes      []string
		wantCandidates []string
	}{
		{
			name:      "text only",
			item:      map[string]any{"commentId": "text", "content": "hello", "contentType": 1},
			wantTypes: []string{"text"},
		},
		{
			name: "direct image",
			item: map[string]any{
				"commentId": "image", "contentType": 2,
				"contentInfo": map[string]any{"imageInfos": []any{
					map[string]any{"Url": "https://media.example.test/image.png?token=private"},
				}},
			},
			wantTypes:      []string{"image"},
			wantCandidates: []string{"image"},
		},
		{
			name: "sticker overrides text content type",
			item: map[string]any{
				"commentId": "sticker", "contentType": 1,
				"contentInfo": map[string]any{"emoticonInfos": []any{
					map[string]any{
						"Url":        "https://media.example.test/sticker.png?token=private",
						"EncryptUrl": "https://media.example.test/encrypted?token=private",
						"AesKey":     "private-key-material",
					},
				}},
			},
			wantTypes:      []string{"sticker"},
			wantCandidates: []string{"sticker"},
		},
		{
			name: "text image and sticker",
			item: map[string]any{
				"commentId": "mixed", "content": "look", "contentType": 1,
				"contentInfo": map[string]any{
					"imageInfos": []any{map[string]any{"Url": "https://media.example.test/a"}},
					"emoticonInfos": []any{map[string]any{
						"Url": "https://media.example.test/b", "EncryptUrl": "ciphertext", "AesKey": "key-material",
					}},
				},
			},
			wantTypes:      []string{"text", "image", "sticker"},
			wantCandidates: []string{"image", "sticker"},
		},
		{
			name: "empty arrays use legacy code",
			item: map[string]any{
				"commentId": "empty", "contentType": 2,
				"contentInfo": map[string]any{"imageInfos": []any{}, "emoticonInfos": []any{}},
			},
			wantTypes: []string{"image"},
		},
		{
			name: "malformed descriptors classify without candidates",
			item: map[string]any{
				"commentId": "malformed", "contentType": 1,
				"contentInfo": map[string]any{"imageInfos": []any{"not-an-object"}},
			},
			wantTypes: []string{"image"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workID := "work-1"
			comment, candidates, _ := mapComment(
				test.item,
				1,
				&workID,
				nil,
				SourceRef{Method: commentListMethod, Ordinal: 1},
			)
			if !reflect.DeepEqual(comment.Content.MediaTypes, test.wantTypes) {
				t.Fatalf("media types=%v want=%v", comment.Content.MediaTypes, test.wantTypes)
			}
			var gotCandidates []string
			for _, candidate := range candidates {
				gotCandidates = append(gotCandidates, candidate.mediaType)
				if candidate.commentRef == "" || candidate.commentRef != comment.Content.MediaRef {
					t.Fatalf("candidate association=%q comment=%q", candidate.commentRef, comment.Content.MediaRef)
				}
			}
			if !reflect.DeepEqual(gotCandidates, test.wantCandidates) {
				t.Fatalf("candidate types=%v want=%v", gotCandidates, test.wantCandidates)
			}
			raw, err := json.Marshal(comment)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{
				"media.example.test", "token=private", "ciphertext", "key-material", "private-key-material", "EncryptUrl", "AesKey",
			} {
				if strings.Contains(string(raw), forbidden) {
					t.Fatalf("serialized comment leaked %q: %s", forbidden, raw)
				}
			}
		})
	}
}

func TestMapCommentMediaRefIsStableForPublicIdentity(t *testing.T) {
	workID := "work-1"
	item := map[string]any{
		"commentId": "comment-1",
		"contentInfo": map[string]any{"emoticonInfos": []any{
			map[string]any{"Url": "https://media.example.test/sticker.png"},
		}},
	}
	first, _, _ := mapComment(item, 1, &workID, nil, SourceRef{Method: commentListMethod, Ordinal: 1})
	second, _, _ := mapComment(item, 1, &workID, nil, SourceRef{Method: commentListMethod, Ordinal: 99})
	if first.Content.MediaRef == "" || first.Content.MediaRef != second.Content.MediaRef {
		t.Fatalf("media refs differ: %q %q", first.Content.MediaRef, second.Content.MediaRef)
	}
}

func TestCommentMediaResolverDownloadsValidatedImages(t *testing.T) {
	formats := []struct {
		name     string
		mimeType string
		payload  []byte
		ext      string
	}{
		{"png", "image/png", testImageBytes(t, "png"), ".png"},
		{"jpeg", "image/jpeg", testImageBytes(t, "jpeg"), ".jpg"},
		{"gif", "image/gif", testImageBytes(t, "gif"), ".gif"},
		{"webp", "image/webp", testWebPBytes(), ".webp"},
	}
	for _, format := range formats {
		t.Run(format.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", format.mimeType)
				_, _ = writer.Write(format.payload)
			}))
			defer server.Close()
			mediaRoot := filepath.Join(t.TempDir(), "media")
			resolver := newCommentMediaResolver(
				server.Client(),
				mediaRoot,
				CommentMediaLimits{PerComment: 4, PerBatch: 100, PerFileBytes: 1 << 20, Timeout: time.Second},
				testAllowedHosts(t, server.URL),
			)
			asset, issue := resolver.Resolve(commentMediaCandidate{
				commentRef: "sha256:" + strings.Repeat("a", 64),
				mediaType:  "image",
				ordinal:    1,
				directURL:  server.URL + "/asset?token=never-persist",
			})
			if issue != nil {
				t.Fatalf("issue=%+v", issue)
			}
			if asset.MIMEType != format.mimeType || !strings.HasSuffix(asset.RelativePath, format.ext) {
				t.Fatalf("asset=%+v", asset)
			}
			stored, err := os.ReadFile(filepath.Join(filepath.Dir(mediaRoot), filepath.FromSlash(asset.RelativePath)))
			if err != nil || !bytes.Equal(stored, format.payload) {
				t.Fatalf("stored mismatch: err=%v", err)
			}
			raw, err := json.Marshal(asset)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{server.URL, "token=never-persist"} {
				if strings.Contains(string(raw), forbidden) {
					t.Fatalf("asset leaked %q: %s", forbidden, raw)
				}
			}
		})
	}
}

func TestCommentMediaResolverRejectsInvalidAndOversizedResponses(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		payload  []byte
		maximum  int64
	}{
		{"signature mismatch", "image/png", []byte("not-an-image"), 1024},
		{"unsupported mime", "application/octet-stream", testImageBytes(t, "png"), 1024},
		{"oversized", "image/png", append(testImageBytes(t, "png"), make([]byte, 1024)...), 32},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", test.mimeType)
				_, _ = writer.Write(test.payload)
			}))
			defer server.Close()
			mediaRoot := filepath.Join(t.TempDir(), "media")
			resolver := newCommentMediaResolver(
				server.Client(), mediaRoot,
				CommentMediaLimits{PerComment: 4, PerBatch: 100, PerFileBytes: test.maximum, Timeout: time.Second},
				testAllowedHosts(t, server.URL),
			)
			asset, issue := resolver.Resolve(commentMediaCandidate{
				commentRef: "sha256:" + strings.Repeat("b", 64), mediaType: "image", directURL: server.URL,
			})
			if issue == nil || issue.code != "media_unavailable" || asset.RelativePath != "" {
				t.Fatalf("asset=%+v issue=%+v", asset, issue)
			}
			assertNoTemporaryMediaFiles(t, mediaRoot)
		})
	}
}

func TestCommentMediaResolverRejectsCrossHostRedirect(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "image/png")
		_, _ = writer.Write(testImageBytes(t, "png"))
	}))
	defer target.Close()
	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	redirectHost := "localhost:" + targetURL.Port()
	source := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "https://"+redirectHost+"/asset", http.StatusFound)
	}))
	defer source.Close()
	allowed := testAllowedHosts(t, source.URL)
	allowed["localhost"] = struct{}{}
	mediaRoot := filepath.Join(t.TempDir(), "media")
	resolver := newCommentMediaResolver(
		source.Client(), mediaRoot,
		CommentMediaLimits{PerComment: 4, PerBatch: 100, PerFileBytes: 1 << 20, Timeout: time.Second},
		allowed,
	)
	_, issue := resolver.Resolve(commentMediaCandidate{
		commentRef: "sha256:" + strings.Repeat("c", 64), mediaType: "image", directURL: source.URL,
	})
	if issue == nil || issue.code != "media_unavailable" {
		t.Fatalf("issue=%+v", issue)
	}
	assertNoTemporaryMediaFiles(t, mediaRoot)
}

func TestCommentMediaResolverReusesContentAddressedFile(t *testing.T) {
	payload := testImageBytes(t, "png")
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "image/png")
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	mediaRoot := filepath.Join(t.TempDir(), "media")
	resolver := newCommentMediaResolver(
		server.Client(), mediaRoot,
		CommentMediaLimits{PerComment: 4, PerBatch: 100, PerFileBytes: 1 << 20, Timeout: time.Second},
		testAllowedHosts(t, server.URL),
	)
	first, firstIssue := resolver.Resolve(commentMediaCandidate{
		commentRef: "sha256:" + strings.Repeat("d", 64), mediaType: "image", ordinal: 1, directURL: server.URL,
	})
	second, secondIssue := resolver.Resolve(commentMediaCandidate{
		commentRef: "sha256:" + strings.Repeat("e", 64), mediaType: "sticker", ordinal: 1, directURL: server.URL,
	})
	if firstIssue != nil || secondIssue != nil || first.RelativePath != second.RelativePath || first.SourceKey == second.SourceKey {
		t.Fatalf("first=%+v second=%+v issues=%+v/%+v", first, second, firstIssue, secondIssue)
	}
	entries, err := os.ReadDir(mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			files++
		}
	}
	if files != 1 {
		t.Fatalf("media files=%d entries=%v", files, entries)
	}
}

func TestCommentMediaResolverSkipsUnprovenEncryptedSticker(t *testing.T) {
	mediaRoot := filepath.Join(t.TempDir(), "media")
	resolver := newCommentMediaResolver(
		&http.Client{}, mediaRoot,
		CommentMediaLimits{PerComment: 4, PerBatch: 100, PerFileBytes: 1 << 20, Timeout: time.Second},
		map[string]struct{}{"media.example.test": {}},
	)
	asset, issue := resolver.Resolve(commentMediaCandidate{
		commentRef: "sha256:" + strings.Repeat("f", 64),
		mediaType:  "sticker", cipherURL: "https://media.example.test/cipher?token=private", aesKey: "private-key",
	})
	if issue == nil || issue.code != "media_unavailable" || asset.RelativePath != "" {
		t.Fatalf("asset=%+v issue=%+v", asset, issue)
	}
	assertNoTemporaryMediaFiles(t, mediaRoot)
}

func TestCommentMediaResolverEnforcesCandidateLimits(t *testing.T) {
	payload := testImageBytes(t, "png")
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		writer.Header().Set("Content-Type", "image/png")
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	mediaRoot := filepath.Join(t.TempDir(), "media")
	resolver := newCommentMediaResolver(
		server.Client(), mediaRoot,
		CommentMediaLimits{PerComment: 1, PerBatch: 2, PerFileBytes: 1 << 20, Timeout: time.Second},
		testAllowedHosts(t, server.URL),
	)
	firstRef := "sha256:" + strings.Repeat("1", 64)
	secondRef := "sha256:" + strings.Repeat("2", 64)
	if _, issue := resolver.Resolve(commentMediaCandidate{commentRef: firstRef, mediaType: "image", directURL: server.URL}); issue != nil {
		t.Fatalf("first issue=%+v", issue)
	}
	if _, issue := resolver.Resolve(commentMediaCandidate{commentRef: firstRef, mediaType: "sticker", ordinal: 1, directURL: server.URL}); issue == nil {
		t.Fatal("per-comment overflow was accepted")
	}
	if _, issue := resolver.Resolve(commentMediaCandidate{commentRef: secondRef, mediaType: "image", directURL: server.URL}); issue != nil {
		t.Fatalf("second issue=%+v", issue)
	}
	if _, issue := resolver.Resolve(commentMediaCandidate{commentRef: "sha256:" + strings.Repeat("3", 64), mediaType: "image", directURL: server.URL}); issue == nil {
		t.Fatal("batch overflow was accepted")
	}
	if calls != 2 {
		t.Fatalf("network calls=%d", calls)
	}
}

func TestCommentMediaResolverRejectsUnsafeURLsWithoutRequest(t *testing.T) {
	mediaRoot := filepath.Join(t.TempDir(), "media")
	resolver := newCommentMediaResolver(
		&http.Client{}, mediaRoot,
		CommentMediaLimits{PerComment: 4, PerBatch: 100, PerFileBytes: 1 << 20, Timeout: time.Second},
		map[string]struct{}{"media.example.test": {}},
	)
	for index, rawURL := range []string{
		"http://media.example.test/asset",
		"https://user:password@media.example.test/asset",
		"https://foreign.example.test/asset",
		"https://media.example.test/asset#fragment",
	} {
		_, issue := resolver.Resolve(commentMediaCandidate{
			commentRef: "sha256:" + strings.Repeat(strconv.Itoa(index+4), 64),
			mediaType:  "image", directURL: rawURL,
		})
		if issue == nil || issue.code != "media_unavailable" {
			t.Fatalf("URL %q issue=%+v", rawURL, issue)
		}
	}
	assertNoTemporaryMediaFiles(t, mediaRoot)
}

func TestCommentMediaResolverHonorsTimeout(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	mediaRoot := filepath.Join(t.TempDir(), "media")
	resolver := newCommentMediaResolver(
		server.Client(), mediaRoot,
		CommentMediaLimits{PerComment: 4, PerBatch: 100, PerFileBytes: 1 << 20, Timeout: 20 * time.Millisecond},
		testAllowedHosts(t, server.URL),
	)
	started := time.Now()
	_, issue := resolver.Resolve(commentMediaCandidate{
		commentRef: "sha256:" + strings.Repeat("9", 64), mediaType: "image", directURL: server.URL,
	})
	if issue == nil || time.Since(started) > time.Second {
		t.Fatalf("issue=%+v elapsed=%s", issue, time.Since(started))
	}
	assertNoTemporaryMediaFiles(t, mediaRoot)
}

func testAllowedHosts(t *testing.T, rawURL string) map[string]struct{} {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		t.Fatalf("parse URL: %v", err)
	}
	return map[string]struct{}{parsed.Hostname(): {}}
}

func testImageBytes(t *testing.T, format string) []byte {
	t.Helper()
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))
	frame.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var output bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&output, frame)
	case "jpeg":
		err = jpeg.Encode(&output, frame, nil)
	case "gif":
		err = gif.Encode(&output, frame, nil)
	default:
		t.Fatalf("unsupported test format %q", format)
	}
	if err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testWebPBytes() []byte {
	return []byte{
		'R', 'I', 'F', 'F', 22, 0, 0, 0, 'W', 'E', 'B', 'P',
		'V', 'P', '8', 'X', 10, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}
}

func assertNoTemporaryMediaFiles(t *testing.T, mediaRoot string) {
	t.Helper()
	_ = filepath.WalkDir(mediaRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			t.Fatal(err)
		}
		if entry != nil && !entry.IsDir() {
			t.Fatalf("unexpected media file after failure: %s", path)
		}
		return nil
	})
}
