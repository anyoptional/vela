package jinx

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func joinPath(prefix, path string) string {
	joined := strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(path, "/")
	if joined == "" {
		return "/"
	}
	return joined
}

func buildURL(ins Instance, path string, pathParams map[string]string) (string, error) {
	path, err := substitutePathParams(path, pathParams)
	if err != nil {
		return "", err
	}
	return buildURLFromPath(ins, path), nil
}

func substitutePathParams(path string, pathParams map[string]string) (string, error) {
	if path == "" {
		path = "/"
	}

	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if !strings.HasPrefix(segment, ":") {
			continue
		}
		name := strings.TrimPrefix(segment, ":")
		value, ok := pathParams[name]
		if !ok {
			return "", fmt.Errorf("jinx: missing path param %q", name)
		}
		segments[i] = url.PathEscape(value)
	}
	return strings.Join(segments, "/"), nil
}

func buildURLFromPath(ins Instance, path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	scheme := "http"
	if ins.Secure {
		scheme = "https"
	}
	host := net.JoinHostPort(ins.Host, strconv.Itoa(ins.Port))
	return scheme + "://" + host + path
}
