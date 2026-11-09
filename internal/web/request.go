package web

import (
	"net/url"
	"strconv"
	"strings"
)

// request is the parsed query string of the page.
type request struct {
	chatID     int64
	query      string
	everywhere bool
	deleted    bool
	offset     int
	around     int64
}

// parseRequest reads the page parameters from v. It reports false when a
// parameter is malformed.
func parseRequest(v url.Values) (request, bool) {
	r := request{
		query:      strings.TrimSpace(v.Get("q")),
		everywhere: v.Get("everywhere") == "1",
		deleted:    v.Get("deleted") == "1",
	}

	var err error
	if s := v.Get("chat"); s != "" {
		if r.chatID, err = strconv.ParseInt(s, 10, 64); err != nil {
			return r, false
		}
	}
	if s := v.Get("around"); s != "" {
		if r.around, err = strconv.ParseInt(s, 10, 64); err != nil || r.chatID == 0 {
			return r, false
		}
	}
	if s := v.Get("offset"); s != "" {
		if r.offset, err = strconv.Atoi(s); err != nil || r.offset < 0 {
			return r, false
		}
	}
	return r, true
}

// url builds a link to the page for this request.
func (r request) url() string {
	v := url.Values{}
	if r.chatID != 0 {
		v.Set("chat", strconv.FormatInt(r.chatID, 10))
	}
	if r.query != "" {
		v.Set("q", r.query)
	}
	if r.everywhere && r.query != "" && r.chatID != 0 {
		v.Set("everywhere", "1")
	}
	if r.deleted {
		v.Set("deleted", "1")
	}
	if r.offset > 0 {
		v.Set("offset", strconv.Itoa(r.offset))
	}
	if r.around != 0 {
		v.Set("around", strconv.FormatInt(r.around, 10))
	}
	if len(v) == 0 {
		return "/"
	}

	link := "/?" + v.Encode()
	if r.around != 0 {
		link += "#target"
	}
	return link
}
