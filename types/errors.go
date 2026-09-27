// Copyright the olareg contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package types

import (
	"encoding/json"
	"errors"
	"net/http"
)

var (
	// ErrBlobExists is returned when attempting to create a blob that already exists.
	ErrBlobExists = errors.New("blob exists")
	// ErrBlobUnknown is returned when the blob is unknown to the registry.
	ErrBlobUnknown = errors.New("blob unknown")
	// ErrBlobUploadInvalid is returned when the blob upload is invalid.
	ErrBlobUploadInvalid = errors.New("blob upload invalid")
	// ErrBlobUploadUnknown is returned when the blob upload is unknown to registry.
	ErrBlobUploadUnknown = errors.New("blob upload unknown to registry")
	// ErrDigestInvalid is returned when the provided digest did not match the uploaded content.
	ErrDigestInvalid = errors.New("provided digest did not match uploaded content")
	// ErrForbidden is returned when an action is not allowed.
	ErrForbidden = errors.New("forbidden")
	// ErrManifestBlobUnknown is returned when the manifest references a manifest or blob unknown to the registry.
	ErrManifestBlobUnknown = errors.New("manifest references a manifest or blob unknown to registry")
	// ErrManifestInvalid is returned when the manifest is invalid.
	ErrManifestInvalid = errors.New("manifest invalid")
	// ErrManifestUnknown is returned when the manifest unknown to the registry.
	ErrManifestUnknown = errors.New("manifest unknown to registry")
	// ErrNameInvalid is returned when the repository name is invalid.
	ErrNameInvalid = errors.New("invalid repository name")
	// ErrNameUnknown is returned when the repository name is not known to the registry.
	ErrNameUnknown = errors.New("NAME_UNKNOWN")
	// ErrNotFound is returned when a resource is not found.
	ErrNotFound = errors.New("not found")
	// ErrPaginationSkippedDescriptor is returned when a descriptor is too large to be included in pagination.
	ErrPaginationSkippedDescriptor = errors.New("pagination skipped a descriptor")
	// ErrParsingFailed is used to indicate the input not be parsed.
	ErrParsingFailed = errors.New("parsing failed")
	// ErrReadOnly is returned when the storage system does not permit write access.
	ErrReadOnly = errors.New("read only storage")
	// ErrRepoNotAllowed is used when a repository name is not permitted.
	ErrRepoNotAllowed = errors.New("repository name is not permitted")
	// ErrSizeInvalid is returned when provided length did not match the content length.
	ErrSizeInvalid = errors.New("provided length did not match content length")
	// ErrUnauthorized is returned when authentication is required.
	ErrUnauthorized = errors.New("authentication required")
	// ErrDenied is returned when the requested access to the resource is denied.
	ErrDenied = errors.New("requested access to the resource is denied")
	// ErrUnsupported is returned when the operation is unsupported.
	ErrUnsupported = errors.New("the operation is unsupported")
	// ErrTooManyRequests is returned when there are too many requests.
	ErrTooManyRequests = errors.New("too many requests")
)

// ErrorResp is returned by the registry on an invalid request.
type ErrorResp struct {
	Errors []ErrorInfo `json:"errors"`
}

// ErrRespJSON encodes a list of errors to json and outputs them to the writer.
func ErrRespJSON(w http.ResponseWriter, errList ...ErrorInfo) error {
	resp := ErrorResp{
		Errors: errList,
	}
	w.Header().Add("content-type", "application/json")
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
