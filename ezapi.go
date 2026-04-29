// Package ezapi can help you call api easier.
//
// 修正自 v1.0.3，主要變更：
//   - TLS 預設不再 InsecureSkipVerify。
//   - Do() 一律 defer resp.Body.Close()，且不在 defer 內覆寫命名 err。
//   - URL + URLQuery 改走 url.Parse 拼接，正確處理已有 query 與編碼。
//   - 全域 Client 不再被 Do() 修改 Timeout，改由每次請求的 context 控制。
//   - Header 寫入 request 時保留多值。
//   - 檔案上傳：固定 defer fh.Close()，fieldname 與 filename 分開，filename 走 filepath.Base。
//   - Timeout 新增 time.Duration 版；0 代表不設 timeout。原本的 TimeOut(int seconds) 保留為 Deprecated。
//   - 拼字修正：initClinet → initClient，且不再是 dead code。
//
// 對外可見的破壞性變更只有：Upload 多了一個 field 參數的 UploadField 版本，
// 原本的 Upload(path) 簽名仍可運作，預設 field 名稱改為 "file"，filename 取 filepath.Base。
package ezapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 套件層級的預設 TLS / Transport / Client。
//
// 注意：預設不再 InsecureSkipVerify。
// 若呼叫端確實需要忽略憑證驗證（例如測試環境的自簽憑證），
// 請自行覆寫 ezapi.TLSConfig，或透過 (*EzAPI).WithClient 傳入自訂 *http.Client。
var (
	TLSConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	TR = &http.Transport{
		TLSClientConfig: TLSConfig,
	}
	// DefaultClient 是 package 級的預設 client；Do() 不會在執行時修改它的欄位。
	DefaultClient = &http.Client{
		Transport: TR,
	}
)

const defaultTimeout = 10 * time.Second

// EzAPI is the main struct of this package.
type EzAPI struct {
	header     http.Header
	raw        []byte
	form       url.Values
	urlquery   url.Values
	json       []byte
	url        string
	bodyMode   bodyMode
	timeout    time.Duration
	timeoutSet bool
	files      []uploadFile
	client     *http.Client
}

type bodyMode int

const (
	bodyNone bodyMode = iota
	bodyRaw
	bodyJSON
	bodyForm     // application/x-www-form-urlencoded
	bodyFormData // multipart/form-data
)

type uploadFile struct {
	field string
	path  string
}

// Rspn - contains response data.
type Rspn struct {
	Header     http.Header
	Body       []byte
	StatusCode int
}

// New create an EzAPI object.
func New() *EzAPI {
	return &EzAPI{}
}

// WithClient 用自訂的 *http.Client 取代預設 client。傳 nil 代表恢復為預設。
func (ez *EzAPI) WithClient(c *http.Client) *EzAPI {
	ez.client = c
	return ez
}

// URL set url.
func (ez *EzAPI) URL(u string) *EzAPI {
	ez.url = u
	return ez
}

// Header add head by a http.Header object.
// 多次呼叫會以最後一次傳入的 header 為準。傳入 nil 等於清空。
func (ez *EzAPI) Header(h http.Header) *EzAPI {
	if h == nil {
		ez.header = nil
		return ez
	}
	ez.header = h.Clone()
	return ez
}

// Raw - text only (no Content-Type set automatically).
func (ez *EzAPI) Raw(body []byte) *EzAPI {
	ez.raw = body
	ez.bodyMode = bodyRaw
	return ez
}

// JSON add json of []byte (sets Content-Type: application/json).
func (ez *EzAPI) JSON(body []byte) *EzAPI {
	ez.json = body
	ez.bodyMode = bodyJSON
	return ez
}

// Form add x-www-form-urlencoded body (sets Content-Type accordingly).
func (ez *EzAPI) Form(form url.Values) *EzAPI {
	ez.form = cloneValues(form)
	ez.bodyMode = bodyForm
	return ez
}

// FormData add multipart/form-data body.
func (ez *EzAPI) FormData(form url.Values) *EzAPI {
	ez.form = cloneValues(form)
	ez.bodyMode = bodyFormData
	return ez
}

// URLQuery add query string into url.
func (ez *EzAPI) URLQuery(q url.Values) *EzAPI {
	ez.urlquery = cloneValues(q)
	return ez
}

// Upload 加入一個要透過 multipart/form-data 上傳的檔案。
// fieldname 預設為 "file"；filename 為該檔案的 base name。
// 若先前 bodyMode 為 none/form，會自動切到 form-data。
func (ez *EzAPI) Upload(path string) *EzAPI {
	return ez.UploadField("file", path)
}

// UploadField 同 Upload，但可指定 multipart 的 field 名稱。
func (ez *EzAPI) UploadField(field, path string) *EzAPI {
	if field == "" {
		field = "file"
	}
	ez.files = append(ez.files, uploadFile{field: field, path: path})
	if ez.bodyMode == bodyNone || ez.bodyMode == bodyForm {
		ez.bodyMode = bodyFormData
	}
	return ez
}

// Timeout 設定請求 timeout。傳 0 代表不設 timeout（一直等到伺服器或 transport 結束）。
// 沒呼叫過 Timeout 時，Do() 會使用 defaultTimeout (= 10s)。
func (ez *EzAPI) Timeout(d time.Duration) *EzAPI {
	ez.timeout = d
	ez.timeoutSet = true
	return ez
}

// TimeOut 是舊版 API，保留向後相容；單位為秒。
//
// Deprecated: use Timeout(time.Duration) instead.
func (ez *EzAPI) TimeOut(seconds int) *EzAPI {
	return ez.Timeout(time.Duration(seconds) * time.Second)
}

// Do execute the http request.
func (ez *EzAPI) Do(method string) (Rspn, error) {
	var rspn Rspn

	if method == "" {
		return rspn, errors.New("ezapi: method is empty")
	}
	if ez.url == "" {
		return rspn, errors.New("ezapi: url is empty")
	}

	urlStr, err := buildURL(ez.url, ez.urlquery)
	if err != nil {
		return rspn, err
	}

	body, contentType, err := ez.buildBody()
	if err != nil {
		return rspn, err
	}

	timeout := ez.timeout
	if !ez.timeoutSet {
		timeout = defaultTimeout
	}

	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return rspn, err
	}

	// 寫 header 時保留多值。Content-Type 由 body builder 決定。
	for k, vs := range ez.header {
		req.Header[k] = append([]string(nil), vs...)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	c := ez.client
	if c == nil {
		c = DefaultClient
	}

	resp, err := c.Do(req)
	if err != nil {
		return rspn, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return rspn, err
	}

	return Rspn{
		StatusCode: resp.StatusCode,
		Body:       respBody,
		Header:     resp.Header,
	}, nil
}

// buildURL 把 query 合併到 url 上。若 url 已經有 query，會以 & 接續而不是覆蓋。
func buildURL(rawURL string, q url.Values) (string, error) {
	if len(q) == 0 {
		return rawURL, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("ezapi: parse url: %w", err)
	}
	existing := u.Query()
	for k, vs := range q {
		for _, v := range vs {
			existing.Add(k, v)
		}
	}
	u.RawQuery = existing.Encode()
	return u.String(), nil
}

// buildBody 根據 bodyMode 組 request body 與 Content-Type。
// 注意：HTTP 規範允許 GET 帶 body，這裡不主動阻擋；如果 server 不支援，呼叫端自行注意。
func (ez *EzAPI) buildBody() (io.Reader, string, error) {
	switch ez.bodyMode {
	case bodyNone:
		return nil, "", nil
	case bodyRaw:
		if len(ez.raw) == 0 {
			return nil, "", nil
		}
		return bytes.NewReader(ez.raw), "", nil
	case bodyJSON:
		if len(ez.json) == 0 {
			return nil, "", nil
		}
		return bytes.NewReader(ez.json), "application/json", nil
	case bodyForm:
		return strings.NewReader(ez.form.Encode()), "application/x-www-form-urlencoded", nil
	case bodyFormData:
		buf, ctype, err := buildMultipart(ez.form, ez.files)
		if err != nil {
			return nil, "", err
		}
		return buf, ctype, nil
	default:
		return nil, "", fmt.Errorf("ezapi: unknown body mode %d", ez.bodyMode)
	}
}

func buildMultipart(form url.Values, files []uploadFile) (*bytes.Buffer, string, error) {
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)

	for k, vs := range form {
		for _, v := range vs {
			if err := w.WriteField(k, v); err != nil {
				return nil, "", fmt.Errorf("ezapi: write form field %q: %w", k, err)
			}
		}
	}

	for _, f := range files {
		if err := writeFilePart(w, f); err != nil {
			return nil, "", err
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("ezapi: close multipart writer: %w", err)
	}
	return buf, w.FormDataContentType(), nil
}

func writeFilePart(w *multipart.Writer, f uploadFile) error {
	fh, err := os.Open(f.path)
	if err != nil {
		return fmt.Errorf("ezapi: open %q: %w", f.path, err)
	}
	defer fh.Close()

	part, err := w.CreateFormFile(f.field, filepath.Base(f.path))
	if err != nil {
		return fmt.Errorf("ezapi: create form file %q: %w", f.path, err)
	}
	if _, err = io.Copy(part, fh); err != nil {
		return fmt.Errorf("ezapi: copy file %q: %w", f.path, err)
	}
	return nil
}

func cloneValues(v url.Values) url.Values {
	if v == nil {
		return nil
	}
	out := make(url.Values, len(v))
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// IsTimeout 判斷錯誤是不是 timeout 造成。
// 同時涵蓋 context.DeadlineExceeded 與 net.Error.Timeout()。
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return false
}
