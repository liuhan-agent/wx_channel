package poc

import "testing"

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
