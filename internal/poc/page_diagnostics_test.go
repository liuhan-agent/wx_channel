package poc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSummarizeCommentMediaShapeWithoutPrivateValues(t *testing.T) {
	diagnostic := summarizePageResponse(commentListMethod, []byte(`{"data":{"data":{"commentInfo":[
		{"content":"private-comment-body","contentType":2,"contentInfo":{"imageInfos":[{"url":"https://mmbiz.qpic.cn/image?token=private-token","sensitive-dynamic-field":"private-value"}]},"levelTwoComment":[
			{"contentType":1,"contentInfo":{"emoticonInfos":[{"EncryptUrl":"https://emoji.qpic.cn/sticker?token=private-token","AesKey":"private-key-material","Url":"  "}]}}
		]},
		{"contentType":2},
		{"contentType":1,"contentInfo":{"imageInfos":[],"emoticonInfos":[null,"bad-descriptor",{"Url":123,"aesKey":null}]}}
	]}}}`))
	raw, err := json.Marshal(diagnostic)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Counts map[string]int `json:"comment_media_shape_counts"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{
		"contentType:2":                                     2,
		"contentType:1":                                     2,
		"contentInfo:missing":                               1,
		"contentInfo:object":                                3,
		"contentInfo.imageInfos:empty":                      1,
		"contentInfo.imageInfos[]:object":                   1,
		"contentInfo.imageInfos[].Url:missing":              1,
		"contentInfo.imageInfos[].url:string":               1,
		"contentInfo.imageInfos[]:candidate:direct":         1,
		"contentInfo.imageInfos[]:other_fields":             1,
		"contentInfo.emoticonInfos:items":                   4,
		"contentInfo.emoticonInfos[]:null":                  1,
		"contentInfo.emoticonInfos[]:string":                1,
		"contentInfo.emoticonInfos[].Url:empty":             1,
		"contentInfo.emoticonInfos[].Url:invalid":           1,
		"contentInfo.emoticonInfos[].EncryptUrl:string":     1,
		"contentInfo.emoticonInfos[].AesKey:string":         1,
		"contentInfo.emoticonInfos[].aesKey:null":           1,
		"contentInfo.emoticonInfos[]:candidate:encrypted":   1,
		"contentInfo.emoticonInfos[]:candidate:unavailable": 1,
	} {
		if decoded.Counts[key] != want {
			t.Errorf("shape %q=%d want=%d", key, decoded.Counts[key], want)
		}
	}
	for _, forbidden := range []string{"private-comment-body", "private-token", "private-key-material", "https://", "sensitive-dynamic-field", "private-value", "bad-descriptor"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("diagnostic leaked %q", forbidden)
		}
	}
}

func TestSummarizePageResponseKeepsShapeWithoutCursorValue(t *testing.T) {
	diagnostic := summarizePageResponse("finderGetCommentList", []byte(`{"data":{"commentInfo":[{"commentId":"secret"}],"countInfo":{"commentCount":1},"lastBuffer":"cursor-secret"}}`))
	if !diagnostic.CommentInfoPresent || diagnostic.CommentInfoType != "array" || diagnostic.CommentInfoCount != 1 {
		t.Fatalf("comment shape=%+v", diagnostic)
	}
	if !diagnostic.CommentCountPresent || diagnostic.ReportedCommentCount != 1 {
		t.Fatalf("comment count=%+v", diagnostic)
	}
	if !diagnostic.LastBufferPresent || diagnostic.LastBufferType != "string" || diagnostic.LastBufferLength != len("cursor-secret") || diagnostic.LastBufferSHA256 == "" {
		t.Fatalf("cursor shape=%+v", diagnostic)
	}
	if diagnostic.LastBufferSHA256 == "cursor-secret" {
		t.Fatal("cursor value leaked")
	}
}

func TestSummarizePageResponseRecognizesLegacySearchCursorAlias(t *testing.T) {
	diagnostic := summarizePageResponse("finderSearch", []byte(`{"data":{"infoList":[],"lastBuff":"legacy-cursor"}}`))
	if !diagnostic.InfoListPresent || diagnostic.InfoListType != "array" || diagnostic.LastBufferLength != len("legacy-cursor") {
		t.Fatalf("search shape=%+v", diagnostic)
	}
}

func TestSummarizePageResponseCountsSearchItemShape(t *testing.T) {
	diagnostic := summarizePageResponse("finderSearch", []byte(`{"data":{"objectList":[
		{"id":"video-1","objectNonceId":"nonce-1","objectDesc":{"mediaType":4}},
		{"id":"other-1","objectDesc":{"mediaType":9}},
		{"objectDesc":{}},
		"invalid"
	],"lastBuffer":"cursor"}}`))
	if diagnostic.ObjectListCount != 4 || diagnostic.ObjectListIDCount != 2 ||
		diagnostic.ObjectListNonceCount != 1 || diagnostic.ObjectListDescCount != 3 ||
		diagnostic.ObjectListVideoCount != 1 || diagnostic.ObjectListUnknownMediaCount != 2 {
		t.Fatalf("search item diagnostics = %+v", diagnostic)
	}
}
