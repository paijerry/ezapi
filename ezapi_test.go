package ezapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGET_WithURLQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Query().Get("a"), "1 2"; got != want {
			t.Errorf("query a = %q, want %q", got, want)
		}
		if got, want := r.URL.Query().Get("b"), "x"; got != want {
			t.Errorf("query b = %q, want %q", got, want)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	rspn, err := New().
		URL(srv.URL + "/path").
		URLQuery(url.Values{"a": {"1 2"}, "b": {"x"}}).
		Do(http.MethodGet)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if rspn.StatusCode != 200 || string(rspn.Body) != "ok" {
		t.Fatalf("unexpected response: status=%d body=%q", rspn.StatusCode, rspn.Body)
	}
}

func TestURLQuery_AppendsToExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("foo") != "bar" || r.URL.Query().Get("a") != "1" {
			t.Errorf("query lost: %s", r.URL.RawQuery)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if _, err := New().
		URL(srv.URL + "/path?foo=bar").
		URLQuery(url.Values{"a": {"1"}}).
		Do(http.MethodGet); err != nil {
		t.Fatal(err)
	}
}

func TestBuildURL_PreservesEncoding(t *testing.T) {
	got, err := buildURL("https://x/p", url.Values{"q": {"a b&c"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://x/p?q=a+b%26c"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("ct = %q", r.Header.Get("Content-Type"))
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"k":1}` {
			t.Errorf("body = %s", b)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	rspn, err := New().URL(srv.URL).JSON([]byte(`{"k":1}`)).Do(http.MethodPost)
	if err != nil {
		t.Fatal(err)
	}
	if rspn.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", rspn.StatusCode)
	}
}

func TestForm_XWWWFormURLEncoded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Content-Type"), "application/x-www-form-urlencoded"; got != want {
			t.Errorf("ct = %q want %q", got, want)
		}
		_ = r.ParseForm()
		if r.PostForm.Get("a") != "1" || r.PostForm.Get("b") != "y" {
			t.Errorf("form = %v", r.PostForm)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if _, err := New().
		URL(srv.URL).
		Form(url.Values{"a": {"1"}, "b": {"y"}}).
		Do(http.MethodPost); err != nil {
		t.Fatal(err)
	}
}

func TestFormData_MultipartUpload(t *testing.T) {
	tmp, err := os.CreateTemp("", "ezapi-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString("hello-file"); err != nil {
		t.Fatal(err)
	}
	_ = tmp.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse: %v", err)
			return
		}
		if r.MultipartForm.Value["a"][0] != "1" {
			t.Errorf("text field a = %v", r.MultipartForm.Value["a"])
		}
		fhs := r.MultipartForm.File["upload"]
		if len(fhs) != 1 {
			t.Fatalf("file count = %d", len(fhs))
		}
		if got, want := fhs[0].Filename, filepath.Base(tmp.Name()); got != want {
			t.Errorf("filename = %q, want %q", got, want)
		}
		f, _ := fhs[0].Open()
		defer f.Close()
		b, _ := io.ReadAll(f)
		if string(b) != "hello-file" {
			t.Errorf("file body = %q", b)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if _, err = New().
		URL(srv.URL).
		FormData(url.Values{"a": {"1"}}).
		UploadField("upload", tmp.Name()).
		Do(http.MethodPost); err != nil {
		t.Fatal(err)
	}
}

func TestUpload_DefaultsToFileFieldAndAutoSwitchesMode(t *testing.T) {
	tmp, err := os.CreateTemp("", "ezapi-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	_, _ = tmp.Write([]byte("zzz"))
	_ = tmp.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse: %v", err)
			return
		}
		if len(r.MultipartForm.File["file"]) != 1 {
			t.Errorf("file field missing: %v", r.MultipartForm.File)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if _, err := New().URL(srv.URL).Upload(tmp.Name()).Do(http.MethodPost); err != nil {
		t.Fatal(err)
	}
}

func TestHeader_PreservesMultipleValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Values("X-Multi")
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("X-Multi = %v", got)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	h := http.Header{}
	h.Add("X-Multi", "a")
	h.Add("X-Multi", "b")

	if _, err := New().URL(srv.URL).Header(h).Do(http.MethodGet); err != nil {
		t.Fatal(err)
	}
}

func TestTimeout_ReturnsErrorAndDetectable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, err := New().URL(srv.URL).Timeout(30 * time.Millisecond).Do(http.MethodGet)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !IsTimeout(err) {
		t.Errorf("IsTimeout = false, err = %v", err)
	}
	// 至少 net.Error.Timeout() 該為 true。
	var ne interface{ Timeout() bool }
	if !errors.As(err, &ne) || !ne.Timeout() {
		// 退而求其次：errors.Is(context.DeadlineExceeded)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err is not a timeout: %v", err)
		}
	}
}

func TestZeroTimeoutMeansNoTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if _, err := New().URL(srv.URL).Timeout(0).Do(http.MethodGet); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestErrors(t *testing.T) {
	if _, err := New().URL("http://example").Do(""); err == nil {
		t.Errorf("expected method error")
	}
	if _, err := New().Do(http.MethodGet); err == nil {
		t.Errorf("expected url error")
	}
}

func TestParallel_NoDataRace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(strings.Repeat("a", 10)))
	}))
	defer srv.Close()

	const N = 32
	var wg sync.WaitGroup
	wg.Add(N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, err := New().URL(srv.URL).Timeout(2 * time.Second).Do(http.MethodGet)
			errs[i] = err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("req %d: %v", i, err)
		}
	}
}

func TestRaw_NoContentTypeSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Raw 不應自動帶 Content-Type
		if ct := r.Header.Get("Content-Type"); ct != "" {
			t.Errorf("unexpected Content-Type %q", ct)
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != "raw-bytes" {
			t.Errorf("body = %q", b)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if _, err := New().URL(srv.URL).Raw([]byte("raw-bytes")).Do(http.MethodPost); err != nil {
		t.Fatal(err)
	}
}
