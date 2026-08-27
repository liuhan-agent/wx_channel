package poc

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
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
