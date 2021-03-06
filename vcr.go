// Package vcr provides an http client that can record and
// playback responses to http requests.
package vcr

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
)

// Mode is used to signify the current operating mode of VCR.
type mode int

const (
	_           = iota
	play   mode = iota // use pre-recorded responses to http requests
	record             // make live requests and record the responses
	live               // make live requests (do not record the responses)
)

var (
	// ErrInvalidMode is returned when mode is not play, record, or live.
	ErrInvalidMode = errors.New("invalid mode")
)

// VCR is an http client that can record and playback responses to http requests.
type VCR struct {
	dir   string
	mode  mode
	seqno int
	Debug bool
}

// New creates a new VCR that will use dir for response storage.
func New(dir string) *VCR {
	return &VCR{
		dir:  dir,
		mode: play,
	}
}

// Play switches the VCR's mode to playback.
func (v *VCR) Play() *VCR {
	v.mode = play
	v.seqno = 0
	return v
}

// Record switches the VCR's mode to record.
func (v *VCR) Record() *VCR {
	v.mode = record
	return v
}

// Live switches the VCR's mode to live.
func (v *VCR) Live() *VCR {
	v.mode = live
	return v
}

// SetDir changes the storage directory.
func (v *VCR) SetDir(dir string) {
	v.dir = dir
	v.seqno = 0
}

// Do makes an http request.
func (v *VCR) Do(req *http.Request) (resp *http.Response, err error) {
	filename, err := v.doFilename(req)
	if err != nil {
		return nil, err
	}

	defer v.incSeqno()

	switch v.mode {
	case play:
		return v.play(filename)
	case record:
		return v.recordDo(req, filename)
	case live:
		return v.liveDo(req)
	}

	return nil, ErrInvalidMode
}

// Get makes an http get request to url.
func (v *VCR) Get(url string) (resp *http.Response, err error) {
	filename, err := v.getFilename(url)
	if err != nil {
		return nil, err
	}

	defer v.incSeqno()

	switch v.mode {
	case play:
		return v.play(filename)
	case record:
		return v.recordGet(url, filename)
	case live:
		return v.liveGet(url)
	}

	return nil, ErrInvalidMode
}

// PostForm posts data to url.
func (v *VCR) PostForm(url string, data url.Values) (resp *http.Response, err error) {
	filename, err := v.postFormFilename(url, data)
	if err != nil {
		return nil, err
	}

	defer v.incSeqno()

	switch v.mode {
	case play:
		return v.play(filename)
	case record:
		return v.recordPostForm(url, data, filename)
	case live:
		return v.livePostForm(url, data)
	}

	return nil, ErrInvalidMode
}

func (v *VCR) incSeqno() {
	v.seqno++
}

func (v *VCR) play(filename string) (*http.Response, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	return v.decodeResponse(data)
}

func (v *VCR) recordDo(req *http.Request, filename string) (*http.Response, error) {
	resp, err := v.liveDo(req)
	return v.writeResponse(resp, filename, err)
}

func (v *VCR) liveDo(req *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(req)
}

func (v *VCR) recordGet(url, filename string) (*http.Response, error) {
	resp, err := v.liveGet(url)
	return v.writeResponse(resp, filename, err)
}

func (v *VCR) liveGet(url string) (*http.Response, error) {
	return http.DefaultClient.Get(url)
}

func (v *VCR) recordPostForm(url string, data url.Values, filename string) (*http.Response, error) {
	resp, err := v.livePostForm(url, data)
	return v.writeResponse(resp, filename, err)
}

func (v *VCR) livePostForm(url string, data url.Values) (*http.Response, error) {
	return http.DefaultClient.PostForm(url, data)
}

func (v *VCR) reqHash(req *http.Request) (string, error) {
	dump, err := httputil.DumpRequestOut(req, true)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(dump)
	return hex.EncodeToString(sum[:]), nil
}

func (v *VCR) urlHash(url string) (string, error) {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:]), nil
}

func (v *VCR) doFilename(req *http.Request) (string, error) {
	hash, err := v.reqHash(req)
	if err != nil {
		return "", err
	}
	return v.filename("do", hash), nil
}

func (v *VCR) getFilename(url string) (string, error) {
	hash, err := v.urlHash(url)
	if err != nil {
		return "", err
	}
	return v.filename("get", hash), nil
}

func (v *VCR) postFormFilename(url string, data url.Values) (string, error) {
	// fmt.Printf("postFormFilename url: %s\n", url)
	// fmt.Printf("postFormFilename data: %s\n", data.Encode())
	h := sha256.New()
	if _, err := h.Write([]byte(url)); err != nil {
		return "", err
	}
	if _, err := h.Write([]byte(data.Encode())); err != nil {
		return "", err
	}
	hash := hex.EncodeToString(h.Sum(nil))
	return v.filename("postform", hash), nil
}

func (v *VCR) filename(prefix, hash string) string {
	return filepath.Join(v.dir, fmt.Sprintf("%s_%s_%d.vcr", prefix, hash, v.seqno))
}

func (v *VCR) encodeResponse(resp *http.Response) ([]byte, error) {
	return httputil.DumpResponse(resp, true)
}

func (v *VCR) decodeResponse(data []byte) (*http.Response, error) {
	buf := bytes.NewBuffer(data)
	return http.ReadResponse(bufio.NewReader(buf), nil)
}

func (v *VCR) writeResponse(resp *http.Response, filename string, reqErr error) (*http.Response, error) {
	if resp == nil {
		return nil, reqErr
	}

	enc, err := v.encodeResponse(resp)
	if err != nil {
		if reqErr != nil {
			return nil, reqErr
		}
		return nil, err
	}

	if v.Debug {
		_, err = os.Stat(filename)
		if !os.IsNotExist(err) {
			fmt.Printf("warning: file %s exists and will be overwritten\n", filename)
			existing, err := ioutil.ReadFile(filename)
			if err != nil {
				return nil, err
			}
			fmt.Printf("existing content:\n%s\n", existing)
			fmt.Printf("new content:\n%s\n", enc)
		}
	}

	if err := ioutil.WriteFile(filename, enc, 0644); err != nil {
		if reqErr != nil {
			return nil, reqErr
		}
		return nil, err
	}

	return resp, reqErr
}
