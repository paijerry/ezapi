//Package ezapi can help you call api easier
package ezapi

import (
	"bytes"
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
)

//EzAPI is the main struct of this package
type EzAPI struct {
	header   http.Header
	form     url.Values
	urlquery url.Values
	json     []byte
	url      string
	xwww     bool
}

//Rspn - contains response data
type Rspn struct {
	Header     http.Header
	Body       string
	StatusCode int
}

//New create an api object
func New() *EzAPI {
	return &EzAPI{}
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

//Form add form("application/x-www-form-urlencoded") by a url.Value object ("Content-Type", "application/x-www-form-urlencoded")
func (ez *EzAPI) Form(form url.Values) *EzAPI {
	ez.xwww = true
	return ez.FormData(form)
}

//FormData add formdata by a url.Value object (no Content-Type)
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

//URL set url
func (ez *EzAPI) URL(url string) *EzAPI {
	ez.url = url
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
	client := &http.Client{}

	urlQuery, err := url.QueryUnescape(ez.urlquery.Encode())

	if err != nil {
		return rspn, err
	}
	urlStr := ez.url + urlQuery

	var req *http.Request

	switch {
	case method == "GET":
		req, err = http.NewRequest(method, urlStr, nil)
	case ez.json != nil:
		req, err = http.NewRequest(method, urlStr, bytes.NewBuffer(ez.json))
		req.Header.Add("Content-Type", `application/json`)
	default:
		req, err = http.NewRequest(method, urlStr, strings.NewReader(ez.form.Encode()))
	}
	if err != nil {
		return rspn, err
	}
	if ez.xwww == true {
		req.Header.Add("Content-Type", `application/x-www-form-urlencoded`)
	}
	for k, v := range ez.header {
		for _, v2 := range v {
			req.Header.Add(k, v2)
		}
	}

	apiResp, err := client.Do(req)
	if err != nil {
		return rspn, err
	}
	defer apiResp.Body.Close()

	respBody, err := ioutil.ReadAll(apiResp.Body)
	if err != nil {
		return rspn, err
	}
	rspn = Rspn{
		StatusCode: apiResp.StatusCode,
		Body:       string(respBody),
		Header:     apiResp.Header,
	}

	return rspn, err
}
