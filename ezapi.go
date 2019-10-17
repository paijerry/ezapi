//Package ezapi can help you call api easier
package ezapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

//EzAPI is the main struct of this package
type EzAPI struct {
	header   http.Header
	raw      []byte
	form     url.Values
	urlquery url.Values
	json     []byte
	url      string
	xwww     bool
	timeout  time.Duration
	filepath []string
}

//Rspn - contains response data
type Rspn struct {
	Header     http.Header
	Body       []byte
	StatusCode int
}

//New create an EzAPI object
func New() *EzAPI {
	return &EzAPI{}
}

//URL set url
func (ez *EzAPI) URL(url string) *EzAPI {
	ez.url = url
	return ez
}

//Header add head by a http.Header object
func (ez *EzAPI) Header(header http.Header) *EzAPI {
	ez.header = http.Header{}
	for k, v := range header {
		for _, v2 := range v {
			ez.header.Add(k, v2)
		}
	}
	return ez
}

// Raw - text only
func (ez *EzAPI) Raw(body []byte) *EzAPI {
	ez.raw = body
	return ez
}

//Form add form("application/x-www-form-urlencoded") by a url.Values object ("Content-Type", "application/x-www-form-urlencoded")
func (ez *EzAPI) Form(form url.Values) *EzAPI {
	ez.xwww = true
	return ez.FormData(form)
}

//FormData add formdata by a url.Values object (no Content-Type)
func (ez *EzAPI) FormData(form url.Values) *EzAPI {
	ez.form = url.Values{}
	for k, v := range form {
		for _, v2 := range v {
			ez.form.Add(k, v2)
		}
	}
	return ez
}

//JSON add json of []byte
func (ez *EzAPI) JSON(body []byte) *EzAPI {
	ez.json = body
	return ez
}

//URLQuery add urlquery into url
func (ez *EzAPI) URLQuery(urlquery url.Values) *EzAPI {
	ez.urlquery = urlquery
	return ez
}

// Upload -
func (ez *EzAPI) Upload(filePath string) *EzAPI {
	ez.filepath = append(ez.filepath, filePath)
	return ez
}

//TimeOut set timeout
func (ez *EzAPI) TimeOut(timeout int) *EzAPI {
	ez.timeout = time.Duration(timeout) * time.Second
	return ez
}

//Do the http request
func (ez *EzAPI) Do(method string) (rspn Rspn, err error) {
	switch {
	case method == "":
		return rspn, errors.New("NO METHOD")
	case ez.url == "":
		return rspn, errors.New("NO URL")
	}

	urlQuery, err := url.QueryUnescape(ez.urlquery.Encode())
	if err != nil {
		return rspn, err
	}
	urlStr := ez.url + urlQuery

	var req *http.Request

	switch {
	case method == "GET": // GET
		req, err = http.NewRequest(method, urlStr, nil)
		if err != nil {
			return rspn, err
		}
	case ez.raw != nil:
		req, err = http.NewRequest(method, urlStr, bytes.NewBuffer(ez.raw))
		if err != nil {
			return rspn, err
		}
	case ez.json != nil: // json
		req, err = http.NewRequest(method, urlStr, bytes.NewBuffer(ez.json))
		if err != nil {
			return rspn, err
		}
		req.Header.Set("Content-Type", `application/json`)
	case ez.xwww: // x-www-form-urlencoded
		req, err = http.NewRequest(method, urlStr, strings.NewReader(ez.form.Encode()))
		if err != nil {
			return rspn, err
		}
		req.Header.Set("Content-Type", `application/x-www-form-urlencoded`)
	default: // form-data

		// form-data: Writer
		var cType string
		var bodyBuf *bytes.Buffer
		cType, bodyBuf, err = formData(ez.form, ez.filepath)
		if err != nil {
			return
		}

		req, err = http.NewRequest(method, urlStr, bodyBuf)
		if err != nil {
			return rspn, err
		}

		req.Header.Set("Content-Type", cType)
	}

	for k, v := range ez.header {
		for _, v2 := range v {
			req.Header.Set(k, v2)
		}
	}

	// timeout context
	if ez.timeout == 0 {
		ez.timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), ez.timeout)
	defer cancel()
	req = req.WithContext(ctx)

	apiResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return rspn, err
	}
	defer func() {
		if err == nil {
			return
		}
		if err.Error() == context.DeadlineExceeded.Error() {
			return
		}

		_, err = io.Copy(ioutil.Discard, apiResp.Body)
		if err != nil {
			log.Printf("%+v", err)
			return
		}
		err = apiResp.Body.Close()
		if err != nil {
			log.Printf("%+v", err)
			return
		}
	}()

	respBody, err := ioutil.ReadAll(apiResp.Body)
	if err != nil {
		return rspn, err
	}
	rspn = Rspn{
		StatusCode: apiResp.StatusCode,
		Body:       respBody,
		Header:     apiResp.Header,
	}

	return rspn, err
}

func formData(form url.Values, fileList []string) (cType string, bodyBuf *bytes.Buffer, err error) {
	bodyBuf = &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(bodyBuf)
	var fileWriter, fw io.Writer

	// Text form-data
	for k, v := range form {
		if fw, err = bodyWriter.CreateFormField(k); err != nil {
			return
		}
		for i := 0; i < len(v); i++ {
			if _, err = fw.Write([]byte(v[i])); err != nil {
				return
			}
		}
	}

	// File form-data
	for i := 0; i < len(fileList); i++ {
		fileWriter, err = bodyWriter.CreateFormFile(fileList[i], fileList[i])
		if err != nil {
			fmt.Println("error writing to buffer")
			return
		}

		// open file handle
		fh, err2 := os.Open(fileList[i])
		if err2 != nil {
			fmt.Println("error opening file")
			return
		}

		//iocopy
		_, err2 = io.Copy(fileWriter, fh)
		if err2 != nil {
			return
		}
	}

	cType = bodyWriter.FormDataContentType()
	bodyWriter.Close()

	return
}
