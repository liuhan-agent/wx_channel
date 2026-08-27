package poc

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	commentMediaMaxRedirects = 3
	commentMediaMaxDimension = 16_384
	commentMediaMaxPixels    = 100_000_000
)

type CommentMediaLimits struct {
	PerComment   int
	PerBatch     int
	PerFileBytes int64
	Timeout      time.Duration
}

type BatchMediaAsset struct {
	CommentRef   string `json:"comment_ref"`
	MediaType    string `json:"media_type"`
	SourceKey    string `json:"source_key"`
	RelativePath string `json:"relative_path"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
	MIMEType     string `json:"mime_type"`
}

type commentMediaResolutionIssue struct {
	code string
}

type commentMediaResolver struct {
	client          *http.Client
	mediaRoot       string
	temporaryRoot   string
	limits          CommentMediaLimits
	allowedHosts    map[string]struct{}
	perCommentCount map[string]int
	batchCount      int
	initErr         error
}

func newCommentMediaResolver(client *http.Client, mediaRoot string, limits CommentMediaLimits, allowedHosts map[string]struct{}) *commentMediaResolver {
	resolver := &commentMediaResolver{
		client:          client,
		mediaRoot:       filepath.Clean(mediaRoot),
		temporaryRoot:   filepath.Join(filepath.Clean(mediaRoot), ".tmp"),
		limits:          limits,
		allowedHosts:    normalizeCommentMediaHosts(allowedHosts),
		perCommentCount: make(map[string]int),
	}
	resolver.initErr = resolver.initialize()
	return resolver
}

func (r *commentMediaResolver) Resolve(candidate commentMediaCandidate) (BatchMediaAsset, *commentMediaResolutionIssue) {
	return r.ResolveContext(context.Background(), candidate)
}

func (r *commentMediaResolver) ResolveContext(ctx context.Context, candidate commentMediaCandidate) (BatchMediaAsset, *commentMediaResolutionIssue) {
	if r == nil || r.initErr != nil || ctx == nil || !validCommentMediaCandidate(candidate) {
		return unavailableCommentMedia()
	}
	if r.batchCount >= r.limits.PerBatch || r.perCommentCount[candidate.commentRef] >= r.limits.PerComment {
		return unavailableCommentMedia()
	}
	r.batchCount++
	r.perCommentCount[candidate.commentRef]++
	if candidate.directURL == "" {
		return unavailableCommentMedia()
	}

	temp, err := os.CreateTemp(r.temporaryRoot, ".media-*.tmp")
	if err != nil {
		return unavailableCommentMedia()
	}
	tempPath := temp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temp.Close()
		}
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return unavailableCommentMedia()
	}

	requestContext, cancel := context.WithTimeout(ctx, r.limits.Timeout)
	defer cancel()
	response, err := r.open(requestContext, candidate.directURL)
	if err != nil {
		return unavailableCommentMedia()
	}
	defer response.Body.Close()
	digest, prefix, byteSize, err := writeBoundedCommentMedia(temp, response, r.limits.PerFileBytes)
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	closed = true
	if err != nil || closeErr != nil {
		return unavailableCommentMedia()
	}
	mimeType, extension, err := validateCommentMediaFile(tempPath, prefix, response.Header.Get("Content-Type"), byteSize)
	if err != nil {
		return unavailableCommentMedia()
	}
	targetName := digest + extension
	targetPath := filepath.Join(r.mediaRoot, targetName)
	if !pathWithin(r.mediaRoot, targetPath) || filepath.Base(targetPath) != targetName {
		return unavailableCommentMedia()
	}
	if err := publishCommentMediaFile(tempPath, targetPath, digest, byteSize); err != nil {
		return unavailableCommentMedia()
	}

	sourceDigest := sha256.Sum256([]byte(strings.Join([]string{
		candidate.mediaType,
		candidate.commentRef,
		digest,
		strconv.Itoa(candidate.ordinal),
	}, "\x00")))
	return BatchMediaAsset{
		CommentRef:   candidate.commentRef,
		MediaType:    candidate.mediaType,
		SourceKey:    candidate.mediaType + ":" + hex.EncodeToString(sourceDigest[:]),
		RelativePath: filepath.ToSlash(filepath.Join("media", targetName)),
		SHA256:       digest,
		Bytes:        byteSize,
		MIMEType:     mimeType,
	}, nil
}

func (r *commentMediaResolver) initialize() error {
	if r.client == nil || r.mediaRoot == "." || r.mediaRoot == "" ||
		r.limits.PerComment <= 0 || r.limits.PerBatch <= 0 ||
		r.limits.PerFileBytes <= 0 || r.limits.Timeout <= 0 ||
		len(r.allowedHosts) == 0 {
		return errors.New("invalid comment media resolver")
	}
	parent := filepath.Dir(r.mediaRoot)
	if err := requireRealDirectory(parent); err != nil {
		return err
	}
	if err := makeOrValidatePrivateDirectory(r.mediaRoot); err != nil {
		return err
	}
	return makeOrValidatePrivateDirectory(r.temporaryRoot)
}

func (r *commentMediaResolver) open(ctx context.Context, rawURL string) (*http.Response, error) {
	parsed, err := validateCommentMediaURL(rawURL, r.allowedHosts)
	if err != nil {
		return nil, err
	}
	client := *r.client
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) == 0 || len(via) > commentMediaMaxRedirects {
			return errors.New("media redirect rejected")
		}
		validated, validateErr := validateCommentMediaURL(request.URL.String(), r.allowedHosts)
		if validateErr != nil || !strings.EqualFold(validated.Hostname(), via[len(via)-1].URL.Hostname()) {
			return errors.New("media redirect rejected")
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, errors.New("media request rejected")
	}
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("media request failed")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, errors.New("media response rejected")
	}
	if encoding := strings.TrimSpace(response.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		response.Body.Close()
		return nil, errors.New("media response rejected")
	}
	return response, nil
}

func writeBoundedCommentMedia(temp *os.File, response *http.Response, maximum int64) (string, []byte, int64, error) {
	if response.ContentLength > maximum {
		return "", nil, 0, errors.New("media exceeds limit")
	}
	digest := sha256.New()
	prefix := make([]byte, 0, 64)
	reader := bufio.NewReader(io.LimitReader(response.Body, maximum+1))
	buffer := make([]byte, 64*1024)
	var total int64
	for {
		count, readErr := reader.Read(buffer)
		if count > 0 {
			total += int64(count)
			if total > maximum {
				return "", nil, total, errors.New("media exceeds limit")
			}
			if len(prefix) < cap(prefix) {
				remaining := cap(prefix) - len(prefix)
				if remaining > count {
					remaining = count
				}
				prefix = append(prefix, buffer[:remaining]...)
			}
			if _, err := digest.Write(buffer[:count]); err != nil {
				return "", nil, total, err
			}
			if _, err := temp.Write(buffer[:count]); err != nil {
				return "", nil, total, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", nil, total, readErr
		}
	}
	if total <= 0 || (response.ContentLength >= 0 && response.ContentLength != total) {
		return "", nil, total, errors.New("media response length mismatch")
	}
	return hex.EncodeToString(digest.Sum(nil)), prefix, total, nil
}

func validateCommentMediaFile(path string, prefix []byte, declared string, byteSize int64) (string, string, error) {
	mimeType, extension := detectCommentMediaType(prefix)
	declaredType, _, err := mime.ParseMediaType(declared)
	if err != nil || mimeType == "" || !strings.EqualFold(declaredType, mimeType) {
		return "", "", errors.New("media MIME mismatch")
	}
	width, height, err := commentMediaDimensions(path, mimeType, prefix)
	if err != nil || width <= 0 || height <= 0 || width > commentMediaMaxDimension || height > commentMediaMaxDimension ||
		int64(width)*int64(height) > commentMediaMaxPixels || byteSize <= 0 {
		return "", "", errors.New("media dimensions rejected")
	}
	return mimeType, extension, nil
}

func detectCommentMediaType(prefix []byte) (string, string) {
	switch {
	case len(prefix) >= 3 && prefix[0] == 0xff && prefix[1] == 0xd8 && prefix[2] == 0xff:
		return "image/jpeg", ".jpg"
	case len(prefix) >= 8 && string(prefix[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png", ".png"
	case len(prefix) >= 6 && (string(prefix[:6]) == "GIF87a" || string(prefix[:6]) == "GIF89a"):
		return "image/gif", ".gif"
	case len(prefix) >= 12 && string(prefix[:4]) == "RIFF" && string(prefix[8:12]) == "WEBP":
		return "image/webp", ".webp"
	default:
		return "", ""
	}
}

func commentMediaDimensions(path, mimeType string, prefix []byte) (int, int, error) {
	if mimeType == "image/webp" {
		return webPDimensions(prefix)
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	configuration, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}
	return configuration.Width, configuration.Height, nil
}

func webPDimensions(prefix []byte) (int, int, error) {
	if len(prefix) < 30 || string(prefix[:4]) != "RIFF" || string(prefix[8:12]) != "WEBP" {
		return 0, 0, errors.New("invalid WebP")
	}
	switch string(prefix[12:16]) {
	case "VP8X":
		width := 1 + int(prefix[24]) + int(prefix[25])<<8 + int(prefix[26])<<16
		height := 1 + int(prefix[27]) + int(prefix[28])<<8 + int(prefix[29])<<16
		return width, height, nil
	case "VP8L":
		if prefix[20] != 0x2f {
			return 0, 0, errors.New("invalid WebP")
		}
		width := 1 + int(prefix[21]) + int(prefix[22]&0x3f)<<8
		height := 1 + int(prefix[22]>>6) + int(prefix[23])<<2 + int(prefix[24]&0x0f)<<10
		return width, height, nil
	case "VP8 ":
		if len(prefix) < 30 || prefix[23] != 0x9d || prefix[24] != 0x01 || prefix[25] != 0x2a {
			return 0, 0, errors.New("invalid WebP")
		}
		width := int(binary.LittleEndian.Uint16(prefix[26:28]) & 0x3fff)
		height := int(binary.LittleEndian.Uint16(prefix[28:30]) & 0x3fff)
		return width, height, nil
	default:
		return 0, 0, errors.New("unsupported WebP")
	}
}

func publishCommentMediaFile(tempPath, targetPath, digest string, byteSize int64) error {
	if _, err := os.Lstat(targetPath); err == nil {
		if err := verifyExistingCommentMediaFile(targetPath, digest, byteSize); err != nil {
			return err
		}
		return os.Remove(tempPath)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tempPath, targetPath); err == nil {
		return nil
	}
	if err := verifyExistingCommentMediaFile(targetPath, digest, byteSize); err != nil {
		return err
	}
	return os.Remove(tempPath)
}

func verifyExistingCommentMediaFile(path, expectedDigest string, expectedSize int64) error {
	if err := requireRegularFileWithoutReparse(path, expectedSize); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	written, err := io.Copy(digest, file)
	if err != nil || written != expectedSize || hex.EncodeToString(digest.Sum(nil)) != expectedDigest {
		return errors.New("existing media file mismatch")
	}
	return nil
}

func validateCommentMediaURL(rawURL string, allowedHosts map[string]struct{}) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	host := ""
	if parsed != nil {
		host = strings.ToLower(parsed.Hostname())
	}
	if err != nil || parsed == nil || parsed.Scheme != "https" || host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("media URL rejected")
	}
	if _, allowed := allowedHosts[host]; !allowed {
		return nil, errors.New("media host rejected")
	}
	return parsed, nil
}

func normalizeCommentMediaHosts(configured map[string]struct{}) map[string]struct{} {
	if configured == nil {
		configured = map[string]struct{}{
			"mmbiz.qpic.cn": {}, "emoji.qpic.cn": {}, "mmbiz.qlogo.cn": {},
			"wx.qlogo.cn": {}, "thirdwx.qlogo.cn": {}, "res.wx.qq.com": {},
		}
	}
	normalized := make(map[string]struct{}, len(configured))
	for host := range configured {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			normalized[host] = struct{}{}
		}
	}
	return normalized
}

func validCommentMediaCandidate(candidate commentMediaCandidate) bool {
	if candidate.mediaType != "image" && candidate.mediaType != "sticker" {
		return false
	}
	if !strings.HasPrefix(candidate.commentRef, "sha256:") || len(candidate.commentRef) != len("sha256:")+64 {
		return false
	}
	digestText := strings.TrimPrefix(candidate.commentRef, "sha256:")
	_, err := hex.DecodeString(digestText)
	return err == nil && digestText == strings.ToLower(digestText) && candidate.ordinal >= 0
}

func unavailableCommentMedia() (BatchMediaAsset, *commentMediaResolutionIssue) {
	return BatchMediaAsset{}, &commentMediaResolutionIssue{code: "media_unavailable"}
}

func makeOrValidatePrivateDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	return requireRealDirectory(path)
}

func requireRealDirectory(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || !samePath(abs, resolved) {
		return errors.New("media directory is unsafe")
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("media directory is unsafe")
	}
	if err := rejectPlatformReparse(abs); err != nil {
		return errors.New("media directory is unsafe")
	}
	return nil
}
