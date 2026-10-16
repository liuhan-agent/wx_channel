package poc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// PageDiagnostic records response shape only. It deliberately excludes field
// values; cursor values are represented by length and a one-way digest.
type PageDiagnostic struct {
	Method                      string         `json:"method"`
	ResponseBytes               int            `json:"response_bytes"`
	ContainerPath               string         `json:"container_path,omitempty"`
	ObjectListPresent           bool           `json:"object_list_present,omitempty"`
	ObjectListType              string         `json:"object_list_type,omitempty"`
	ObjectListCount             int            `json:"object_list_count,omitempty"`
	ObjectListIDCount           int            `json:"object_list_id_count,omitempty"`
	ObjectListNonceCount        int            `json:"object_list_nonce_count,omitempty"`
	ObjectListDescCount         int            `json:"object_list_desc_count,omitempty"`
	ObjectListVideoCount        int            `json:"object_list_video_count,omitempty"`
	ObjectListUnknownMediaCount int            `json:"object_list_unknown_media_count,omitempty"`
	ObjectDescKeyNames          []string       `json:"object_desc_key_names,omitempty"`
	MediaTypeShapeCounts        map[string]int `json:"media_type_shape_counts,omitempty"`
	MediaTypeCodeCounts         map[string]int `json:"media_type_code_counts,omitempty"`
	MediaFieldShapeCounts       map[string]int `json:"media_field_shape_counts,omitempty"`
	InfoListPresent             bool           `json:"info_list_present,omitempty"`
	InfoListType                string         `json:"info_list_type,omitempty"`
	InfoListCount               int            `json:"info_list_count,omitempty"`
	CommentInfoPresent          bool           `json:"comment_info_present,omitempty"`
	CommentInfoType             string         `json:"comment_info_type,omitempty"`
	CommentInfoCount            int            `json:"comment_info_count,omitempty"`
	CommentMediaShapeCounts     map[string]int `json:"comment_media_shape_counts,omitempty"`
	LastBufferPresent           bool           `json:"last_buffer_present"`
	LastBufferType              string         `json:"last_buffer_type,omitempty"`
	LastBufferLength            int            `json:"last_buffer_length,omitempty"`
	LastBufferSHA256            string         `json:"last_buffer_sha256,omitempty"`
	CommentCountPresent         bool           `json:"comment_count_present,omitempty"`
	CommentCountType            string         `json:"comment_count_type,omitempty"`
	ReportedCommentCount        int            `json:"reported_comment_count,omitempty"`
}

func summarizePageResponse(method string, raw []byte) PageDiagnostic {
	diagnostic := PageDiagnostic{Method: method, ResponseBytes: len(raw)}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var root map[string]any
	if decoder.Decode(&root) != nil {
		return diagnostic
	}
	data, ok := root["data"].(map[string]any)
	if !ok {
		return diagnostic
	}
	container := data
	diagnostic.ContainerPath = "data"
	if nested, ok := data["data"].(map[string]any); ok {
		container = nested
		diagnostic.ContainerPath = "data.data"
	}
	setList := func(value any, present *bool, typeName *string, count *int) {
		*present = true
		*typeName = jsonType(value)
		if items, ok := value.([]any); ok {
			*count = len(items)
		}
	}
	if value, exists := container["objectList"]; exists {
		setList(value, &diagnostic.ObjectListPresent, &diagnostic.ObjectListType, &diagnostic.ObjectListCount)
		if items, ok := value.([]any); ok {
			descKeys := make(map[string]struct{})
			diagnostic.MediaTypeShapeCounts = make(map[string]int)
			diagnostic.MediaTypeCodeCounts = make(map[string]int)
			diagnostic.MediaFieldShapeCounts = make(map[string]int)
			for _, rawItem := range items {
				item, ok := rawItem.(map[string]any)
				if !ok {
					continue
				}
				if id, ok := stringField(item, "id"); ok && id != "" {
					diagnostic.ObjectListIDCount++
				}
				if nonce, ok := stringField(item, "objectNonceId"); ok && nonce != "" {
					diagnostic.ObjectListNonceCount++
				}
				description, ok := item["objectDesc"].(map[string]any)
				if !ok {
					continue
				}
				diagnostic.ObjectListDescCount++
				for key := range description {
					descKeys[key] = struct{}{}
				}
				if media, exists := description["media"]; exists {
					diagnostic.MediaFieldShapeCounts[jsonType(media)]++
				} else {
					diagnostic.MediaFieldShapeCounts["missing"]++
				}
				mediaType, exists := description["mediaType"]
				if !exists {
					diagnostic.ObjectListUnknownMediaCount++
					continue
				}
				shape := jsonType(mediaType)
				diagnostic.MediaTypeShapeCounts[shape]++
				diagnostic.MediaTypeCodeCounts[safeMediaTypeCode(mediaType)]++
				switch {
				case mapSearchMedia(item).Normalized == "video":
					diagnostic.ObjectListVideoCount++
				default:
					diagnostic.ObjectListUnknownMediaCount++
				}
			}
			for key := range descKeys {
				diagnostic.ObjectDescKeyNames = append(diagnostic.ObjectDescKeyNames, key)
			}
			sort.Strings(diagnostic.ObjectDescKeyNames)
		}
	}
	if value, exists := container["infoList"]; exists {
		setList(value, &diagnostic.InfoListPresent, &diagnostic.InfoListType, &diagnostic.InfoListCount)
	}
	if value, exists := container["commentInfo"]; exists {
		setList(value, &diagnostic.CommentInfoPresent, &diagnostic.CommentInfoType, &diagnostic.CommentInfoCount)
		if items, ok := value.([]any); ok {
			diagnostic.CommentMediaShapeCounts = make(map[string]int)
			for _, value := range items {
				if item, ok := value.(map[string]any); ok {
					summarizeCommentMediaShape(item, diagnostic.CommentMediaShapeCounts)
					for _, reply := range childComments(item) {
						summarizeCommentMediaShape(reply, diagnostic.CommentMediaShapeCounts)
					}
				}
			}
		}
	}
	if value, exists := container["lastBuffer"]; exists {
		diagnostic.LastBufferPresent = true
		diagnostic.LastBufferType = jsonType(value)
		if marker, ok := value.(string); ok {
			diagnostic.LastBufferLength = len(marker)
			digest := sha256.Sum256([]byte(marker))
			diagnostic.LastBufferSHA256 = hex.EncodeToString(digest[:])
		}
	} else if value, exists := container["lastBuff"]; exists {
		diagnostic.LastBufferPresent = true
		diagnostic.LastBufferType = jsonType(value)
		if marker, ok := value.(string); ok {
			diagnostic.LastBufferLength = len(marker)
			digest := sha256.Sum256([]byte(marker))
			diagnostic.LastBufferSHA256 = hex.EncodeToString(digest[:])
		}
	}
	if countInfo, ok := container["countInfo"].(map[string]any); ok {
		if value, exists := countInfo["commentCount"]; exists {
			diagnostic.CommentCountPresent = true
			diagnostic.CommentCountType = jsonType(value)
			switch typed := value.(type) {
			case json.Number:
				if parsed, err := typed.Int64(); err == nil && parsed >= 0 {
					diagnostic.ReportedCommentCount = int(parsed)
				}
			case float64:
				if typed >= 0 && typed == float64(int(typed)) {
					diagnostic.ReportedCommentCount = int(typed)
				}
			}
		}
	}
	return diagnostic
}

// Keep keys in this diagnostic finite: never copy descriptor names or values.
// Both observed PascalCase fields and possible camelCase variants are counted;
// counting a variant does not make it an accepted download descriptor.
func summarizeCommentMediaShape(item map[string]any, counts map[string]int) {
	code := "missing"
	if value, present := item["contentType"]; present {
		code = "other"
		switch fmt.Sprint(value) {
		case "1":
			code = "1"
		case "2":
			code = "2"
		}
	}
	counts["contentType:"+code]++
	value, present := item["contentInfo"]
	if !present {
		counts["contentInfo:missing"]++
		return
	}
	counts["contentInfo:"+jsonType(value)]++
	info, ok := value.(map[string]any)
	if !ok {
		return
	}
	for _, group := range []string{"imageInfos", "emoticonInfos"} {
		path := "contentInfo." + group
		value, present := info[group]
		if !present {
			counts[path+":missing"]++
			continue
		}
		counts[path+":"+jsonType(value)]++
		values, ok := value.([]any)
		if !ok {
			continue
		}
		counts[path+":items"] += len(values)
		if len(values) == 0 {
			counts[path+":empty"]++
		}
		for _, value := range values {
			counts[path+"[]:"+jsonType(value)]++
			descriptor, ok := value.(map[string]any)
			if !ok {
				continue
			}
			knownFields := 0
			for _, key := range []string{"Url", "url", "EncryptUrl", "encryptUrl", "AesKey", "aesKey"} {
				state := "missing"
				if field, present := descriptor[key]; present {
					knownFields++
					switch typed := field.(type) {
					case nil:
						state = "null"
					case string:
						state = "string"
						if strings.TrimSpace(typed) == "" {
							state = "empty"
						}
					default:
						state = "invalid"
					}
				}
				counts[path+"[]."+key+":"+state]++
			}
			counts[path+"[]:other_fields"] += len(descriptor) - knownFields
		}
	}
}

func safeMediaTypeCode(value any) string {
	switch typed := value.(type) {
	case json.Number:
		if len(typed) <= 16 {
			return typed.String()
		}
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	}
	return "non_numeric"
}
