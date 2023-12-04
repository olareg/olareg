package types

import (
	"encoding/json"
	"errors"
	"io"
)

var (
	// ErrNotFound is returned when a resource is not found.
	ErrNotFound = errors.New("not found")
)

// ErrorResp is returned by the registry on an invalid request.
type ErrorResp struct {
	Errors []ErrorInfo `json:"errors"`
}

// ErrRespJSON encodes a list of errors to json and outputs them to the writer.
func ErrRespJSON(w io.Writer, errList ...ErrorInfo) error {
	resp := ErrorResp{
		Errors: errList,
	}
	return json.NewEncoder(w).Encode(resp)
}

// ErrorInfo describes an error entry from [ErrorResp].
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail"`
}

// ErrInfoBlobUnknown is returned when the blob unknown to the registry.
func ErrInfoBlobUnknown(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "BLOB_UNKNOWN",
		Message: "blob unknown to registry",
		Detail:  d,
	}
}

// ErrInfoBlobUploadInvalid is returned when the blob upload is invalid.
func ErrInfoBlobUploadInvalid(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "BLOB_UPLOAD_INVALID",
		Message: "blob upload invalid",
		Detail:  d,
	}
}

// ErrInfoBlobUploadUnknown is returned when the blob upload is unknown to registry.
func ErrInfoBlobUploadUnknown(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "BLOB_UPLOAD_UNKNOWN",
		Message: "blob upload unknown to registry",
		Detail:  d,
	}
}

// ErrInfoDigestInvalid is returned when the provided digest did not match the uploaded content.
func ErrInfoDigestInvalid(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "DIGEST_INVALID",
		Message: "provided digest did not match uploaded content",
		Detail:  d,
	}
}

// ErrInfoManifestBlobUnknown is returned when the manifest references a manifest or blob unknown to the registry.
func ErrInfoManifestBlobUnknown(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "MANIFEST_BLOB_UNKNOWN",
		Message: "manifest references a manifest or blob unknown to registry",
		Detail:  d,
	}
}

// ErrInfoManifestInvalid is returned when the manifest is invalid.
func ErrInfoManifestInvalid(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "MANIFEST_INVALID",
		Message: "manifest invalid",
		Detail:  d,
	}
}

// ErrInfoManifestUnknown is returned when the manifest unknown to the registry.
func ErrInfoManifestUnknown(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "MANIFEST_UNKNOWN",
		Message: "manifest unknown to registry",
		Detail:  d,
	}
}

// ErrInfoNameInvalid is returned when the repository name is invalid.
func ErrInfoNameInvalid(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "NAME_INVALID",
		Message: "invalid repository name",
		Detail:  d,
	}
}

// ErrInfoNameUnknown is returned when the repository name is not known to the registry.
func ErrInfoNameUnknown(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "repository name not known to registry",
		Message: "NAME_UNKNOWN",
		Detail:  d,
	}
}

// ErrInfoSizeInvalid is returned when provided length did not match the content length.
func ErrInfoSizeInvalid(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "SIZE_INVALID",
		Message: "provided length did not match content length",
		Detail:  d,
	}
}

// ErrInfoUnauthorized is returned when authentication is required.
func ErrInfoUnauthorized(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "UNAUTHORIZED",
		Message: "authentication required",
		Detail:  d,
	}
}

// ErrInfoDenied is returned when the requested access to the resource is denied.
func ErrInfoDenied(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "DENIED",
		Message: "requested access to the resource is denied",
		Detail:  d,
	}
}

// ErrInfoUnsupported is returned when the operation is unsupported.
func ErrInfoUnsupported(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "UNSUPPORTED",
		Message: "the operation is unsupported",
		Detail:  d,
	}
}

// ErrInfoTooManyRequests is returned when there are too many requests.
func ErrInfoTooManyRequests(d string) ErrorInfo {
	return ErrorInfo{
		Code:    "TOOMANYREQUESTS",
		Message: "too many requests",
		Detail:  d,
	}
}
