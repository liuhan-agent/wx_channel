package poc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixturePageAPI struct {
	responses [][]byte
	calls     int
}

func (a *fixturePageAPI) Call(context.Context, string, any) ([]byte, error) {
	if a.calls >= len(a.responses) {
		return nil, fmt.Errorf("unexpected API call %d", a.calls+1)
	}
	response := a.responses[a.calls]
	a.calls++
	return response, nil
}

type methodFixturePageAPI struct {
	responses map[string][][]byte
	calls     []string
	bodies    []any
}

func (a *methodFixturePageAPI) Call(_ context.Context, method string, body any) ([]byte, error) {
	a.calls = append(a.calls, method)
	a.bodies = append(a.bodies, body)
	pages := a.responses[method]
	index := 0
	for _, called := range a.calls {
		if called == method {
			index++
		}
	}
	if index == 0 || index > len(pages) {
		return nil, fmt.Errorf("unexpected API call %s #%d", method, index)
	}
	return pages[index-1], nil
}

func TestCollectVideoKeywordSearchesAccountsAndExpandsOnlyVideos(t *testing.T) {
	api := &methodFixturePageAPI{responses: map[string][][]byte{
		"finderSearch": {[]byte(`{"data":{"objectList":[
			{"id":"live-result","objectNonceId":"live-nonce","objectDesc":{"mediaType":9}}
		],"infoList":[
			{"contact":{"username":"finder-alice","nickname":"Alice","profileUrl":"https://channels.weixin.qq.com/web/pages/profile?finderUsername=finder-alice"}},
			{"contact":{"username":"finder-bob","nickname":"Bob","profileUrl":"https://channels.weixin.qq.com/web/pages/profile?finderUsername=finder-bob"}}
		],"lastBuffer":""}}`)},
		"finderUserPage": {
			[]byte(`{"data":{"object":[
				{"id":"alice-video-1","objectNonceId":"alice-nonce-1","objectDesc":{"mediaType":4}},
				{"id":"alice-video-2","objectNonceId":"alice-nonce-2","objectDesc":{"mediaType":4}},
				{"id":"alice-video-3","objectNonceId":"alice-nonce-3","objectDesc":{"mediaType":4}},
				{"id":"alice-video-4","objectNonceId":"alice-nonce-4","objectDesc":{"mediaType":4}},
				{"id":"alice-video-5","objectNonceId":"alice-nonce-5","objectDesc":{"mediaType":4}}
			],"lastBuffer":""}}`),
			[]byte(`{"data":{"object":[
				{"id":"bob-video-1","objectNonceId":"bob-nonce-1","objectDesc":{"mediaType":4}},
				{"id":"bob-video-2","objectNonceId":"bob-nonce-2","objectDesc":{"mediaType":4}},
				{"id":"bob-video-3","objectNonceId":"bob-nonce-3","objectDesc":{"mediaType":4}},
				{"id":"bob-video-4","objectNonceId":"bob-nonce-4","objectDesc":{"mediaType":4}},
				{"id":"bob-video-5","objectNonceId":"bob-nonce-5","objectDesc":{"mediaType":4}}
			],"lastBuffer":""}}`),
		},
	}}
	options := approvedTestOptions()
	options.RequireVideoWorks = true
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "account-keyword-video-job"), &fixtureClock{})
	works, coverage, err := collector.CollectWorks(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 10 || coverage != CoverageTargetMet {
		t.Fatalf("works=%d coverage=%s", len(works), coverage)
	}
	if len(api.bodies) < 1 {
		t.Fatalf("requests=%v", api.bodies)
	}
	searchBody, ok := api.bodies[0].(map[string]any)
	if !ok || searchBody["type"] != 3 {
		t.Fatalf("finderSearch body=%#v, want type 3 video search", api.bodies[0])
	}
	for _, work := range works {
		if work.MediaType.Normalized != "video" {
			t.Fatalf("non-video work collected: %+v", work)
		}
		if work.Author.ProfileURL == nil || !strings.HasPrefix(*work.Author.ProfileURL, "https://channels.weixin.qq.com/web/pages/profile?finderUsername=") {
			t.Fatalf("author profile URL not preserved: %+v", work.Author)
		}
	}
}

func TestMapSearchMediaSeparatesPicturesFromVideos(t *testing.T) {
	for _, test := range []struct {
		code string
		want string
	}{
		{code: "2", want: "picture"},
		{code: "4", want: "video"},
		{code: "9", want: "unknown"},
	} {
		t.Run(test.code, func(t *testing.T) {
			work := mapSearchWork(map[string]any{"objectDesc": map[string]any{"mediaType": test.code}}, "id", "", 1, 1, 1, SourceRef{})
			if work.MediaType.Normalized != test.want {
				t.Fatalf("media type %q normalized to %q, want %q", test.code, work.MediaType.Normalized, test.want)
			}
		})
	}
}

func TestMapSearchAccountCarriesOnlyReturnedProfileURL(t *testing.T) {
	work := mapSearchWork(map[string]any{
		"contact": map[string]any{
			"username":   "author-id",
			"nickname":   "作者",
			"profileUrl": "https://channels.weixin.qq.com/web/pages/profile?finderUsername=author-id",
		},
	}, "work-id", "主题", 1, 1, 1, SourceRef{})
	if work.Author.ProfileURL == nil || *work.Author.ProfileURL != "https://channels.weixin.qq.com/web/pages/profile?finderUsername=author-id" {
		t.Fatalf("profile URL=%v", work.Author.ProfileURL)
	}

	withoutProfile := mapSearchWork(map[string]any{
		"contact": map[string]any{
			"username": "author-id",
			"nickname": "作者",
		},
	}, "work-id-2", "主题", 2, 1, 2, SourceRef{})
	if withoutProfile.Author.ProfileURL != nil {
		t.Fatalf("profile URL guessed from account ID: %v", withoutProfile.Author.ProfileURL)
	}
}

func TestMapSearchWorkExtractsEngagementMetricsAndStatuses(t *testing.T) {
	work := mapSearchWork(map[string]any{
		"likeCount":    "1.2万",
		"commentCount": "17",
		"feedInfo": map[string]any{
			"favCountFmt":     "3.5k",
			"forwardCountFmt": "not-a-count",
		},
	}, "work-id", "主题", 1, 1, 1, SourceRef{})
	if work.Engagement == nil {
		t.Fatal("engagement envelope is nil")
	}
	metrics := work.Engagement.Metrics
	if metrics["like_count"].Status != ContentMetricAvailable ||
		metrics["like_count"].Value == nil || *metrics["like_count"].Value != 12000 {
		t.Fatalf("like metric=%+v", metrics["like_count"])
	}
	if metrics["platform_comment_count"].Status != ContentMetricAvailable ||
		metrics["platform_comment_count"].Value == nil || *metrics["platform_comment_count"].Value != 17 {
		t.Fatalf("comment metric=%+v", metrics["platform_comment_count"])
	}
	if metrics["favorite_count"].Status != ContentMetricAvailable ||
		metrics["favorite_count"].Value == nil || *metrics["favorite_count"].Value != 3500 {
		t.Fatalf("favorite metric=%+v", metrics["favorite_count"])
	}
	if metrics["share_count"].Status != ContentMetricSchemaUnrecognized ||
		metrics["share_count"].Value != nil {
		t.Fatalf("share metric=%+v", metrics["share_count"])
	}
	missing := mapSearchWork(map[string]any{}, "work-id-2", "主题", 2, 1, 2, SourceRef{}).Engagement.Metrics
	for key, metric := range missing {
		if metric.Status != ContentMetricSourceMissing || metric.Value != nil {
			t.Fatalf("missing metric %s=%+v", key, metric)
		}
	}
}

type fixtureClock struct{ now time.Time }

func (c *fixtureClock) Now() time.Time { return c.now }
func (c *fixtureClock) Sleep(ctx context.Context, duration time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		c.now = c.now.Add(duration)
		return nil
	}
}

func TestCollectSearchPreservesOrderAndExhaustion(t *testing.T) {
	api := &fixturePageAPI{responses: [][]byte{readFixture(t, "search-page-1.json"), readFixture(t, "search-page-end.json")}}
	clock := &fixtureClock{}
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "search-job"), clock)
	works, coverage, err := collector.CollectWorks(context.Background(), approvedTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 3 || works[0].Locator.SearchRank != 1 || works[2].Locator.SearchRank != 3 {
		t.Fatalf("works=%+v", works)
	}
	if works[2].MediaType.Normalized != "unknown" {
		t.Fatalf("non-video media=%+v", works[2].MediaType)
	}
	if coverage != CoverageSourceExhausted {
		t.Fatalf("coverage=%s", coverage)
	}
	if clock.now != (time.Time{}).Add(time.Second) {
		t.Fatalf("request spacing=%s", clock.now)
	}
}

func TestEmptyPageWithoutExplicitLastBufferIsNotExhaustion(t *testing.T) {
	api := &fixturePageAPI{responses: [][]byte{[]byte(`{"data":{"objectList":[]}}`)}}
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "empty-job"), &fixtureClock{})
	_, coverage, err := collector.CollectWorks(context.Background(), approvedTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if coverage != CoverageIncomplete {
		t.Fatalf("coverage=%s", coverage)
	}
}

func TestRepeatedSearchMarkerStopsAsIncomplete(t *testing.T) {
	page := []byte(`{"data":{"objectList":[],"lastBuffer":"repeat"}}`)
	api := &fixturePageAPI{responses: [][]byte{page, page}}
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "repeat-job"), &fixtureClock{})
	_, coverage, err := collector.CollectWorks(context.Background(), approvedTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if coverage != CoverageIncomplete || api.calls != 2 {
		t.Fatalf("coverage=%s calls=%d", coverage, api.calls)
	}
}

func TestChangingSearchCursorWithoutNewVideoStopsAsIncomplete(t *testing.T) {
	responses := [][]byte{[]byte(`{"data":{"objectList":[
		{"id":"first-video","objectNonceId":"first-nonce","objectDesc":{"mediaType":4}}
	],"lastBuffer":"search-cursor-1"}}`)}
	for page := 2; page <= maxFinderSearchNoProgressPages+1; page++ {
		responses = append(responses, []byte(fmt.Sprintf(`{"data":{"objectList":[
			{"id":"non-video-%d","objectNonceId":"non-video-nonce-%d","objectDesc":{"mediaType":9}}
		],"lastBuffer":"search-cursor-%d"}}`, page, page, page)))
	}
	api := &fixturePageAPI{responses: responses}
	options := approvedTestOptions()
	options.Limits.Works = 2
	options.RequireVideoWorks = true
	store := newTestStore(t, "changing-search-cursor-job")
	collector := NewCollector(api, NewEvidenceRecorder(nil), store, &fixtureClock{})
	works, coverage, err := collector.CollectWorks(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || coverage != CoverageIncomplete {
		t.Fatalf("works=%d coverage=%s", len(works), coverage)
	}
	if api.calls != maxFinderSearchNoProgressPages+1 {
		t.Fatalf("calls=%d want=%d", api.calls, maxFinderSearchNoProgressPages+1)
	}
	checkpoint, err := store.LoadCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Phase != "search_incomplete" || checkpoint.SearchMarker != "search-cursor-9" {
		t.Fatalf("checkpoint phase=%q marker=%q", checkpoint.Phase, checkpoint.SearchMarker)
	}
}

func TestMaxFinderSearchPagesScalesWithWorkLimit(t *testing.T) {
	if got := maxFinderSearchPages(1); got != minFinderSearchPages {
		t.Fatalf("limit(1)=%d want=%d", got, minFinderSearchPages)
	}
	if got := maxFinderSearchPages(5); got != 50 {
		t.Fatalf("limit(5)=%d want=50", got)
	}
	if got := maxFinderSearchPages(30); got != 300 {
		t.Fatalf("limit(30)=%d want=300", got)
	}
}

func TestSearchStopsAtTenUniqueValidWorks(t *testing.T) {
	items := ""
	for i := 1; i <= 12; i++ {
		if i > 1 {
			items += ","
		}
		items += fmt.Sprintf(`{"id":"fixture-work-%d","objectDesc":{"mediaType":2}}`, i)
	}
	page := []byte(`{"data":{"objectList":[` + items + `],"lastBuffer":"more"}}`)
	api := &fixturePageAPI{responses: [][]byte{page}}
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "limit-job"), &fixtureClock{})
	works, coverage, err := collector.CollectWorks(context.Background(), approvedTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 10 || coverage != CoverageTargetMet || api.calls != 1 {
		t.Fatalf("works=%d coverage=%s calls=%d", len(works), coverage, api.calls)
	}
}

func TestCollectSearchSkipsUnknownMediaWhenVideoWorksRequired(t *testing.T) {
	api := &fixturePageAPI{responses: [][]byte{[]byte(`{"data":{"objectList":[
		{"id":"live-result","objectNonceId":"live-nonce","objectDesc":{"mediaType":9}},
		{"id":"video-result","objectNonceId":"video-nonce","objectDesc":{"mediaType":4}}
	],"lastBuffer":""}}`)}}
	options := approvedTestOptions()
	options.Limits.Works = 1
	options.RequireVideoWorks = true
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "video-only-search-job"), &fixtureClock{})
	works, coverage, err := collector.CollectWorks(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || dereference(works[0].WorkID) != "video-result" || works[0].MediaType.Normalized != "video" {
		t.Fatalf("works=%+v", works)
	}
	if coverage != CoverageTargetMet || api.calls != 1 {
		t.Fatalf("coverage=%s calls=%d", coverage, api.calls)
	}
}

func TestCollectSearchSkipsMediaTypeNineVideoCards(t *testing.T) {
	api := &fixturePageAPI{responses: [][]byte{[]byte(`{"data":{"objectList":[
		{"id":"search-video-9","objectNonceId":"nonce-9","objectDesc":{"mediaType":9,"media":[{"url":"redacted"}]}}
	],"lastBuffer":""}}`)}}
	options := approvedTestOptions()
	options.Limits.Works = 1
	options.RequireVideoWorks = true
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "media-type-nine-search-job"), &fixtureClock{})
	works, coverage, err := collector.CollectWorks(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 0 {
		t.Fatalf("works=%+v", works)
	}
	if coverage != CoverageSourceExhausted || api.calls != 1 {
		t.Fatalf("coverage=%s calls=%d", coverage, api.calls)
	}
}

func TestCollectSearchSkipsMediaTypeNineObjectMedia(t *testing.T) {
	api := &fixturePageAPI{responses: [][]byte{[]byte(`{"data":{"objectList":[
		{"id":"search-video-9-object","objectNonceId":"nonce-9-object","objectDesc":{"mediaType":9,"media":{"url":"redacted"}}}
	],"lastBuffer":""}}`)}}
	options := approvedTestOptions()
	options.Limits.Works = 1
	options.RequireVideoWorks = true
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "media-type-nine-object-search-job"), &fixtureClock{})
	works, coverage, err := collector.CollectWorks(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 0 {
		t.Fatalf("works=%+v", works)
	}
	if coverage != CoverageSourceExhausted || api.calls != 1 {
		t.Fatalf("coverage=%s calls=%d", coverage, api.calls)
	}
}

func TestCollectSearchDoesNotLetMediaTypeNineCardsDisplaceAccountVideos(t *testing.T) {
	api := &methodFixturePageAPI{responses: map[string][][]byte{
		"finderSearch": {[]byte(`{"data":{"objectList":[
			{"id":"live-result","objectNonceId":"live-nonce","objectDesc":{"description":"欢迎来到我的直播间","mediaType":9,"media":[{"url":"redacted"}]}}
		],"infoList":[{"contact":{"username":"finder-account","nickname":"直播账号"}}],"lastBuffer":""}}`)},
		"finderUserPage": {[]byte(`{"data":{"object":[
			{"id":"account-video","objectNonceId":"account-video-nonce","objectDesc":{"description":"普通视频","mediaType":4,"media":[{"url":"redacted"}]}}
		],"lastBuffer":""}}`)},
	}}
	options := approvedTestOptions()
	options.Limits.Works = 1
	options.RequireVideoWorks = true
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "media-type-nine-does-not-displace-video-job"), &fixtureClock{})
	works, coverage, err := collector.CollectWorks(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || dereference(works[0].WorkID) != "account-video" || works[0].Source.Method != "finderUserPage" {
		t.Fatalf("works=%+v", works)
	}
	if coverage != CoverageTargetMet || fmt.Sprint(api.calls) != "[finderSearch finderUserPage]" {
		t.Fatalf("coverage=%s calls=%v", coverage, api.calls)
	}
}

func TestCollectSearchResolvesAccountsThroughFeedList(t *testing.T) {
	api := &methodFixturePageAPI{responses: map[string][][]byte{
		"finderSearch": {[]byte(`{"data":{"infoList":[{"contact":{"username":"finder-alice","nickname":"Alice"}},{"contact":{"username":"finder-bob","nickname":"Bob"}}],"lastBuff":""}}`)},
		"finderUserPage": {
			[]byte(`{"data":{"object":[{"id":"feed-1","objectNonceId":"nonce-1","objectDesc":{"description":"Alice work","mediaType":2}}],"lastBuffer":""}}`),
			[]byte(`{"data":{"object":[{"id":"feed-2","objectNonceId":"nonce-2","objectDesc":{"description":"Bob work","mediaType":2}}],"lastBuffer":""}}`),
		},
	}}
	options := approvedTestOptions()
	options.Limits.Works = 2
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "account-feed-job"), &fixtureClock{})
	works, coverage, err := collector.CollectWorks(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 2 || coverage != CoverageTargetMet {
		t.Fatalf("works=%d coverage=%s", len(works), coverage)
	}
	if works[0].WorkID == nil || *works[0].WorkID != "feed-1" || works[1].WorkID == nil || *works[1].WorkID != "feed-2" {
		t.Fatalf("works=%+v", works)
	}
	if fmt.Sprint(api.calls) != "[finderSearch finderUserPage finderUserPage]" {
		t.Fatalf("calls=%v", api.calls)
	}
}

func TestCollectSearchStopsOnEmptyFeedPageWithCursor(t *testing.T) {
	api := &methodFixturePageAPI{responses: map[string][][]byte{
		"finderSearch": {[]byte(`{"data":{"infoList":[{"contact":{"username":"finder-empty"}}],"lastBuffer":""}}`)},
		"finderUserPage": {
			[]byte(`{"data":{"object":[],"lastBuffer":"feed-cursor-1"}}`),
		},
	}}
	options := approvedTestOptions()
	options.Limits.Works = 1
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "empty-feed-cursor-job"), &fixtureClock{})
	works, coverage, err := collector.CollectWorks(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 0 || coverage != CoverageIncomplete {
		t.Fatalf("works=%d coverage=%s", len(works), coverage)
	}
	if fmt.Sprint(api.calls) != "[finderSearch finderUserPage]" {
		t.Fatalf("calls=%v", api.calls)
	}
}

func TestCollectSearchStopsOnFeedPageWithoutCursor(t *testing.T) {
	api := &methodFixturePageAPI{responses: map[string][][]byte{
		"finderSearch": {[]byte(`{"data":{"infoList":[{"contact":{"username":"finder-missing-cursor"}}],"lastBuffer":""}}`)},
		"finderUserPage": {
			[]byte(`{"data":{"object":[{"id":"feed-without-cursor","objectNonceId":"nonce-1","objectDesc":{"mediaType":2}}]}}`),
		},
	}}
	options := approvedTestOptions()
	options.Limits.Works = 2
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "missing-feed-cursor-job"), &fixtureClock{})
	works, coverage, err := collector.CollectWorks(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || coverage != CoverageIncomplete {
		t.Fatalf("works=%d coverage=%s", len(works), coverage)
	}
	if fmt.Sprint(api.calls) != "[finderSearch finderUserPage]" {
		t.Fatalf("calls=%v", api.calls)
	}
}

func TestCollectSearchStopsAfterFeedNoProgressEvenWithChangingCursors(t *testing.T) {
	api := &methodFixturePageAPI{responses: map[string][][]byte{
		"finderSearch": {[]byte(`{"data":{"infoList":[{"contact":{"username":"finder-no-progress"}}],"lastBuffer":""}}`)},
		"finderUserPage": {
			[]byte(`{"data":{"object":[{"id":"feed-first","objectNonceId":"nonce-1","objectDesc":{"mediaType":2}}],"lastBuffer":"feed-cursor-1"}}`),
			[]byte(`{"data":{"object":[],"lastBuffer":"feed-cursor-2"}}`),
		},
	}}
	options := approvedTestOptions()
	options.Limits.Works = 2
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "changing-feed-cursor-job"), &fixtureClock{})
	works, coverage, err := collector.CollectWorks(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 1 || coverage != CoverageIncomplete {
		t.Fatalf("works=%d coverage=%s", len(works), coverage)
	}
	if fmt.Sprint(api.calls) != "[finderSearch finderUserPage finderUserPage]" {
		t.Fatalf("calls=%v", api.calls)
	}
}

func TestCollectSearchStopsOnFeedCursorThatDoesNotAdvance(t *testing.T) {
	api := &methodFixturePageAPI{responses: map[string][][]byte{
		"finderSearch": {[]byte(`{"data":{"infoList":[{"contact":{"username":"finder-stalled"}}],"lastBuffer":""}}`)},
		"finderUserPage": {
			[]byte(`{"data":{"object":[{"id":"feed-first","objectNonceId":"nonce-1","objectDesc":{"mediaType":2}}],"lastBuffer":"feed-cursor"}}`),
			[]byte(`{"data":{"object":[{"id":"feed-second","objectNonceId":"nonce-2","objectDesc":{"mediaType":2}}],"lastBuffer":"feed-cursor"}}`),
		},
	}}
	options := approvedTestOptions()
	options.Limits.Works = 3
	collector := NewCollector(api, NewEvidenceRecorder(nil), newTestStore(t, "stalled-feed-cursor-job"), &fixtureClock{})
	works, coverage, err := collector.CollectWorks(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 2 || coverage != CoverageIncomplete {
		t.Fatalf("works=%d coverage=%s", len(works), coverage)
	}
	if fmt.Sprint(api.calls) != "[finderSearch finderUserPage finderUserPage]" {
		t.Fatalf("calls=%v", api.calls)
	}
}

func approvedTestOptions() Options {
	options := DefaultOptions()
	options.AckIsolatedVM = true
	return options
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
