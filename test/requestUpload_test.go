package test

import (
	"bytes"
	"encoding/json"
	"expo-open-ota/internal/bucket"
	cache2 "expo-open-ota/internal/cache"
	"expo-open-ota/internal/handlers"
	"expo-open-ota/internal/services"
	"expo-open-ota/internal/update"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func createUploadRequest(t *testing.T, projectRoot, branch, runtimeVersion, sampleUpdatePath, headerKey, headerValue string) (*httptest.ResponseRecorder, *mux.Router, *mux.Route, *http.Request) {
	os.Setenv("LOCAL_BUCKET_BASE_PATH", filepath.Join(projectRoot, "./updates"))
	q := fmt.Sprintf("http://localhost:3000/requestUploadUrl/%s?runtimeVersion=%s", branch, runtimeVersion)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", q, nil)
	r = mux.SetURLVars(r, map[string]string{"BRANCH": branch})
	r.Header.Set(headerKey, headerValue)
	uploadRequestsInput := ComputeUploadRequestsInput(sampleUpdatePath)
	uploadRequestsInputJSON, err := json.Marshal(uploadRequestsInput)
	if err != nil {
		t.Fatalf("Error marshalling uploadRequestsInput: %v", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(uploadRequestsInputJSON))
	return w, mux.NewRouter(), nil, r
}

func performUpload(t *testing.T, projectRoot, branch, runtimeVersion, sampleUpdatePath string) string {
	os.Setenv("LOCAL_BUCKET_BASE_PATH", filepath.Join(projectRoot, "./updates"))
	requestURL := fmt.Sprintf("http://localhost:3000/requestUploadUrl/%s?runtimeVersion=%s", branch, runtimeVersion)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{"BRANCH": branch})
	r.Header.Set("Authorization", "Bearer expo_test_token")

	uploadRequestsInput := ComputeUploadRequestsInput(sampleUpdatePath)
	uploadRequestsInputJSON, err := json.Marshal(uploadRequestsInput)
	if err != nil {
		t.Fatalf("Error marshalling uploadRequestsInput: %v", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(uploadRequestsInputJSON))
	handlers.RequestUploadUrlHandler(w, r)
	if w.Code != 200 {
		t.Fatalf("RequestUploadUrlHandler returned status %d instead of 200", w.Code)
	}

	var responseBody struct {
		UpdateId       int64                      `json:"updateId"`
		UploadRequests []bucket.FileUploadRequest `json:"uploadRequests"`
	}
	if err := json.NewDecoder(w.Body).Decode(&responseBody); err != nil {
		t.Fatalf("Error decoding response body: %v", err)
	}
	updateId := fmt.Sprintf("%d", responseBody.UpdateId)

	fileUploadRequests := responseBody.UploadRequests
	ws := make([]*httptest.ResponseRecorder, len(fileUploadRequests))
	errs := make(chan error, len(fileUploadRequests))
	var wg sync.WaitGroup

	for i, uploadRequest := range fileUploadRequests {
		wg.Add(1)
		go func(index int, req bucket.FileUploadRequest) {
			defer wg.Done()
			ws[index] = httptest.NewRecorder()
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			localFilePath := filepath.Join(sampleUpdatePath, req.FilePath)
			fileBuffer, err := os.Open(localFilePath)
			if err != nil {
				errs <- fmt.Errorf("Error opening file %s: %w", localFilePath, err)
				return
			}
			defer fileBuffer.Close()

			part, err := writer.CreateFormFile(req.FileName, req.FileName)
			if err != nil {
				errs <- fmt.Errorf("Error creating multipart form file: %w", err)
				return
			}
			if _, err = io.Copy(part, fileBuffer); err != nil {
				errs <- fmt.Errorf("Error copying file to multipart part: %w", err)
				return
			}
			if err = writer.Close(); err != nil {
				errs <- fmt.Errorf("Error closing multipart writer: %w", err)
				return
			}

			parsedUrl, err := url.Parse(req.RequestUploadUrl)
			if err != nil {
				errs <- fmt.Errorf("Error parsing URL %s: %w", req.RequestUploadUrl, err)
				return
			}
			token := parsedUrl.Query().Get("token")
			uploadReq := httptest.NewRequest("PUT", "/uploadLocalFile?token="+token, body)
			uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
			uploadReq.Header.Set("Authorization", "Bearer expo_test_token")
			handlers.RequestUploadLocalFileHandler(ws[index], uploadReq)
			if ws[index].Code != 200 {
				errs <- fmt.Errorf("File upload for %s returned status %d", req.FileName, ws[index].Code)
			}
		}(i, uploadRequest)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Error during file uploads: %v", err)
	}
	for _, recorder := range ws {
		if recorder.Code != 200 {
			t.Fatalf("A file upload returned status %d instead of 200", recorder.Code)
		}
		sampleReq := fileUploadRequests[0]
		expectedFilePath := filepath.Join(projectRoot, "updates", branch, runtimeVersion, updateId, sampleReq.FilePath)
		if _, err := os.Open(expectedFilePath); err != nil {
			t.Fatalf("Error opening uploaded file %s: %v", expectedFilePath, err)
		}
	}
	return updateId
}

func markUpdateAsUploaded(t *testing.T, branch, runtimeVersion, updateId string) *httptest.ResponseRecorder {
	markURL := fmt.Sprintf("http://localhost:3000/markUpdateAsUploaded/%s?platform=android&runtimeVersion=%s&updateId=%s", branch, runtimeVersion, updateId)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", markURL, nil)
	r.Header.Set("Authorization", "Bearer expo_test_token")
	r = mux.SetURLVars(r, map[string]string{"BRANCH": branch})
	handlers.MarkUpdateAsUploadedHandler(w, r)
	return w
}

func TestRequestUploadUrlWithoutBearer(t *testing.T) {
	teardown := setup(t)
	defer teardown()
	mockExpoForRequestUploadUrlTest("staging")

	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("Error finding project root: %v", err)
	}
	sampleUpdatePath := filepath.Join(projectRoot, "/test/test-updates/branch-1/1/1674170951")
	w, _, _, r := createUploadRequest(t, projectRoot, "DO_NOT_USE", "1", sampleUpdatePath, "Authorization", "Bearer expo_alternative_token")
	handlers.RequestUploadUrlHandler(w, r)
	assert.Equal(t, 401, w.Code, "Expected status code 401")
	assert.Equal(t, "Invalid expo account\n", w.Body.String(), "Expected error message")
}

func TestRequestUploadUrlWithBadBearer(t *testing.T) {
	teardown := setup(t)
	defer teardown()
	mockExpoForRequestUploadUrlTest("staging")

	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("Error finding project root: %v", err)
	}
	sampleUpdatePath := filepath.Join(projectRoot, "/test/test-updates/branch-1/1/1674170951")
	w, _, _, r := createUploadRequest(t, projectRoot, "DO_NOT_USE", "1", sampleUpdatePath, "Authorization", "Bearer expo_bad_token")
	handlers.RequestUploadUrlHandler(w, r)
	assert.Equal(t, 401, w.Code, "Expected status code 401")
	assert.Equal(t, "Error fetching expo account informations\n", w.Body.String(), "Expected error message")
}

func TestRequestUploadUrlWithoutRuntimeVersion(t *testing.T) {
	teardown := setup(t)
	defer teardown()
	mockExpoForRequestUploadUrlTest("staging")

	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("Error finding project root: %v", err)
	}
	os.Setenv("LOCAL_BUCKET_BASE_PATH", filepath.Join(projectRoot, "./updates"))
	q := "http://localhost:3000/requestUploadUrl/DO_NOT_USE"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", q, nil)
	r = mux.SetURLVars(r, map[string]string{"BRANCH": "DO_NOT_USE"})
	r.Header.Set("Authorization", "Bearer expo_test_token")
	sampleUpdatePath := filepath.Join(projectRoot, "/test/test-updates/branch-1/1/1674170951")
	uploadRequestsInput := ComputeUploadRequestsInput(sampleUpdatePath)
	uploadRequestsInputJSON, err := json.Marshal(uploadRequestsInput)
	if err != nil {
		t.Fatalf("Error marshalling uploadRequestsInput: %v", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(uploadRequestsInputJSON))
	handlers.RequestUploadUrlHandler(w, r)
	assert.Equal(t, 400, w.Code, "Expected status code 400")
	assert.Equal(t, "No runtime version provided\n", w.Body.String(), "Expected error message")
}

func TestRequestUploadUrlWithBadRequestBody(t *testing.T) {
	teardown := setup(t)
	defer teardown()
	mockExpoForRequestUploadUrlTest("staging")

	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("Error finding project root: %v", err)
	}
	os.Setenv("LOCAL_BUCKET_BASE_PATH", filepath.Join(projectRoot, "./updates"))
	q := "http://localhost:3000/requestUploadUrl/DO_NOT_USE?runtimeVersion=1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", q, nil)
	r = mux.SetURLVars(r, map[string]string{"BRANCH": "DO_NOT_USE"})
	r.Header.Set("Authorization", "Bearer expo_test_token")
	uploadRequestsInputJSON, err := json.Marshal(map[string]string{"id": "4"})
	if err != nil {
		t.Fatalf("Error marshalling uploadRequestsInput: %v", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(uploadRequestsInputJSON))
	handlers.RequestUploadUrlHandler(w, r)
	assert.Equal(t, 400, w.Code, "Expected status code 400")
	assert.Equal(t, "No file names provided\n", w.Body.String(), "Expected error message")
}

func TestRequestUploadUrlWithBadFilenamesType(t *testing.T) {
	teardown := setup(t)
	defer teardown()
	mockExpoForRequestUploadUrlTest("staging")

	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("Error finding project root: %v", err)
	}
	os.Setenv("LOCAL_BUCKET_BASE_PATH", filepath.Join(projectRoot, "./updates"))
	q := "http://localhost:3000/requestUploadUrl/DO_NOT_USE?runtimeVersion=1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", q, nil)
	r = mux.SetURLVars(r, map[string]string{"BRANCH": "DO_NOT_USE"})
	r.Header.Set("Authorization", "Bearer expo_test_token")
	uploadRequestsInputJSON, err := json.Marshal(map[string]int{"fileNames": 1})
	if err != nil {
		t.Fatalf("Error marshalling uploadRequestsInput: %v", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(uploadRequestsInputJSON))
	handlers.RequestUploadUrlHandler(w, r)
	assert.Equal(t, 400, w.Code, "Expected status code 400")
	assert.Equal(t, "Invalid JSON body\n", w.Body.String(), "Expected error message")
}

func TestRequestUploadUrlWithSampleUpdate(t *testing.T) {
	teardown := setup(t)
	defer teardown()
	mockExpoForRequestUploadUrlTest("staging")

	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("Error finding project root: %v", err)
	}
	os.Setenv("LOCAL_BUCKET_BASE_PATH", filepath.Join(projectRoot, "./updates"))
	q := "http://localhost:3000/requestUploadUrl/DO_NOT_USE?runtimeVersion=1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", q, nil)
	r = mux.SetURLVars(r, map[string]string{"BRANCH": "DO_NOT_USE"})
	r.Header.Set("Authorization", "Bearer expo_test_token")
	sampleUpdatePath := filepath.Join(projectRoot, "/test/test-updates/branch-4/1/1674170952")
	uploadRequestsInput := ComputeUploadRequestsInput(sampleUpdatePath)
	uploadRequestsInputJSON, err := json.Marshal(uploadRequestsInput)
	if err != nil {
		t.Fatalf("Error marshalling uploadRequestsInput: %v", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(uploadRequestsInputJSON))
	handlers.RequestUploadUrlHandler(w, r)
	assert.Equal(t, 200, w.Code, "Expected status code 200")

	var responseBody struct {
		UpdateId       int64                      `json:"updateId"`
		UploadRequests []bucket.FileUploadRequest `json:"uploadRequests"`
	}
	if err := json.NewDecoder(w.Body).Decode(&responseBody); err != nil {
		assert.Fail(t, "Expected valid JSON response")
	}
	uploadRequests := responseBody.UploadRequests
	assert.Len(t, uploadRequests, 4, "Expected 4 file upload requests")
	updateIdHeader := w.Header().Get("expo-update-id")
	assert.NotEmpty(t, updateIdHeader, "Expected non-empty update ID")

	for _, req := range uploadRequests {
		parsedUrl, err := url.Parse(req.RequestUploadUrl)
		assert.Nil(t, err, "Expected valid URL")
		assert.Equal(t, "http", parsedUrl.Scheme, "Expected HTTP scheme")
		assert.Equal(t, "localhost:3000", parsedUrl.Host, "Expected localhost:3000 host")
		assert.Equal(t, "/uploadLocalFile", parsedUrl.Path, "Expected /uploadLocalFile path")
		token := parsedUrl.Query().Get("token")
		assert.NotEmpty(t, token, "Expected non-empty token")
		claims := jwt.MapClaims{}
		decoded, err := services.DecodeAndExtractJWTToken("test_jwt_secret", token, claims)
		assert.Nil(t, err, "Expected valid JWT token")
		if !decoded.Valid {
			assert.Fail(t, "Expected valid JWT token")
		}
		filePath, ok := claims["filePath"].(string)
		assert.True(t, ok, "Expected filePath to be a string")
		assert.NotEmpty(t, filePath, "Expected non-empty file path")
		sub, ok := claims["sub"].(string)
		assert.True(t, ok, "Expected sub to be a string")
		assert.Equal(t, "test_username", sub, "Expected test_username sub")
	}

	var (
		ws   = make([]*httptest.ResponseRecorder, len(uploadRequests))
		errs = make(chan error, len(uploadRequests))
		wg   sync.WaitGroup
	)
	for i, req := range uploadRequests {
		wg.Add(1)
		go func(index int, uploadReq bucket.FileUploadRequest) {
			defer wg.Done()
			ws[index] = httptest.NewRecorder()
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			filePath := filepath.Join(projectRoot, "/test/test-updates/branch-4/1/1674170952", uploadReq.FilePath)
			fileBuffer, err := os.Open(filePath)
			if err != nil {
				errs <- err
				return
			}
			part, err := writer.CreateFormFile(uploadReq.FileName, uploadReq.FileName)
			if err != nil {
				errs <- err
				return
			}
			_, err = io.Copy(part, fileBuffer)
			if err != nil {
				errs <- err
				return
			}
			_ = writer.Close()
			parsedUrl, err := url.Parse(uploadReq.RequestUploadUrl)
			if err != nil {
				errs <- err
				return
			}
			token := parsedUrl.Query().Get("token")
			uploadFileReq := httptest.NewRequest("PUT", "/uploadLocalFile?token="+token, body)
			uploadFileReq.Header.Set("Content-Type", writer.FormDataContentType())
			uploadFileReq.Header.Set("Authorization", "Bearer expo_test_token")
			handlers.RequestUploadLocalFileHandler(ws[index], uploadFileReq)
			if ws[index].Code != 200 {
				errs <- fmt.Errorf("Upload failed with status %d", ws[index].Code)
			}
		}(i, req)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.Nil(t, err, "Expected no errors")
	}
	for _, rec := range ws {
		assert.Equal(t, 200, rec.Code, "Expected status code 200")
		expectedFile := filepath.Join(projectRoot, "/updates/DO_NOT_USE/1/", updateIdHeader, uploadRequests[0].FilePath)
		if _, err := os.Open(expectedFile); err != nil {
			assert.Nil(t, err, "Expected no errors when opening uploaded file")
		}
	}
	lastUpdate, err := update.GetLatestUpdateBundlePathForRuntimeVersion("DO_NOT_USE", "1")
	if err != nil {
		t.Fatalf("Error getting latest update: %v", err)
	}
	assert.Nil(t, lastUpdate, "Expected nil")
	qMark := "http://localhost:3000/markUpdateAsUploaded/DO_NOT_USE?platform=android&runtimeVersion=1&updateId=" + updateIdHeader
	wMark := httptest.NewRecorder()
	rMark := httptest.NewRequest("POST", qMark, nil)
	rMark.Header.Set("Authorization", "Bearer expo_test_token")
	rMark = mux.SetURLVars(rMark, map[string]string{"BRANCH": "DO_NOT_USE"})
	handlers.MarkUpdateAsUploadedHandler(wMark, rMark)
	assert.Equal(t, 200, wMark.Code, "Expected status code 200")
	lastUpdate, err = update.GetLatestUpdateBundlePathForRuntimeVersion("DO_NOT_USE", "1")
	if err != nil {
		t.Fatalf("Error getting latest update: %v", err)
	}
	assert.NotNil(t, lastUpdate, "Expected non-nil")
	assert.Equal(t, updateIdHeader, lastUpdate.UpdateId, "Expected update ID to match")
}

func TestRequestUploadUrlWithValidExpoSession(t *testing.T) {
	teardown := setup(t)
	defer teardown()
	mockExpoForRequestUploadUrlTest("staging")

	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("Error finding project root: %v", err)
	}
	os.Setenv("LOCAL_BUCKET_BASE_PATH", filepath.Join(projectRoot, "./updates"))
	q := "http://localhost:3000/requestUploadUrl/DO_NOT_USE?runtimeVersion=1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", q, nil)
	r = mux.SetURLVars(r, map[string]string{"BRANCH": "DO_NOT_USE"})
	r.Header.Set("expo-session", "expo_test_session")
	sampleUpdatePath := filepath.Join(projectRoot, "/test/test-updates/branch-1/1/1674170951")
	uploadRequestsInput := ComputeUploadRequestsInput(sampleUpdatePath)
	uploadRequestsInputJSON, err := json.Marshal(uploadRequestsInput)
	if err != nil {
		t.Fatalf("Error marshalling uploadRequestsInput: %v", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(uploadRequestsInputJSON))
	handlers.RequestUploadUrlHandler(w, r)
	assert.Equal(t, 200, w.Code, "Expected status code 200")
	assert.NotEmpty(t, w.Header().Get("expo-update-id"), "Expected non-empty update ID")
}

func TestShouldClearCache(t *testing.T) {
	teardown := setup(t)
	defer teardown()
	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("Error finding project root: %v", err)
	}
	os.Setenv("LOCAL_BUCKET_BASE_PATH", filepath.Join(projectRoot, "/test/test-updates"))
	mockWorkingExpoResponse("staging")
	qManifest := "http://localhost:3000/manifest"
	wManifest := httptest.NewRecorder()
	rManifest := httptest.NewRequest("GET", qManifest, nil)
	rManifest.Header.Add("expo-platform", "ios")
	rManifest.Header.Add("expo-runtime-version", "1")
	rManifest.Header.Add("expo-protocol-version", "1")
	rManifest.Header.Add("expo-expect-signature", "true")
	rManifest.Header.Add("expo-channel-name", "staging")
	handlers.ManifestHandler(wManifest, rManifest)
	assert.Equal(t, 200, wManifest.Code, "Expected status code 200")

	os.Setenv("LOCAL_BUCKET_BASE_PATH", filepath.Join(projectRoot, "./updates"))
	q := "http://localhost:3000/requestUploadUrl/branch-1?runtimeVersion=1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", q, nil)
	r = mux.SetURLVars(r, map[string]string{"BRANCH": "branch-1"})
	r.Header.Set("expo-session", "expo_test_session")
	sampleUpdatePath := filepath.Join(projectRoot, "/test/test-updates/branch-1/1/1674170951")
	cache := cache2.GetCache()
	cacheKey := update.ComputeLastUpdateCacheKey("branch-1", "1")
	value := cache.Get(cacheKey)
	expectedValue := "{\"branch\":\"branch-1\",\"runtimeVersion\":\"1\",\"updateId\":\"1674170951\",\"createdAt\":1674170951000000}"
	assert.Equal(t, expectedValue, value, "Expected a specific cache value")
	uploadRequestsInput := ComputeUploadRequestsInput(sampleUpdatePath)
	uploadRequestsInputJSON, err := json.Marshal(uploadRequestsInput)
	if err != nil {
		t.Fatalf("Error marshalling uploadRequestsInput: %v", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(uploadRequestsInputJSON))
	handlers.RequestUploadUrlHandler(w, r)
	assert.Equal(t, 200, w.Code, "Expected status code 200")
	assert.NotEmpty(t, w.Header().Get("expo-update-id"), "Expected non-empty update ID")
	value = cache.Get(cacheKey)
	assert.Empty(t, value, "Expected an empty cache value")
}

func TestRequestUploadUrlWithInvalidExpoSession(t *testing.T) {
	teardown := setup(t)
	defer teardown()
	mockExpoForRequestUploadUrlTest("staging")

	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("Error finding project root: %v", err)
	}
	os.Setenv("LOCAL_BUCKET_BASE_PATH", filepath.Join(projectRoot, "./updates"))
	q := "http://localhost:3000/requestUploadUrl/DO_NOT_USE?runtimeVersion=1"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", q, nil)
	r = mux.SetURLVars(r, map[string]string{"BRANCH": "DO_NOT_USE"})
	r.Header.Set("expo-session", "invalid_session_token")
	sampleUpdatePath := filepath.Join(projectRoot, "/test/test-updates/branch-1/1/1674170951")
	uploadRequestsInput := ComputeUploadRequestsInput(sampleUpdatePath)
	uploadRequestsInputJSON, err := json.Marshal(uploadRequestsInput)
	if err != nil {
		t.Fatalf("Error marshalling uploadRequestsInput: %v", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(uploadRequestsInputJSON))
	handlers.RequestUploadUrlHandler(w, r)
	assert.Equal(t, 401, w.Code, "Expected status code 401")
	assert.Equal(t, "Error fetching expo account informations\n", w.Body.String(), "Expected error message")
}

func TestIdenticalUpload(t *testing.T) {
	teardown := setup(t)
	defer teardown()
	mockExpoForRequestUploadUrlTest("staging")

	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("Error finding project root: %v", err)
	}
	sampleUpdatePath := filepath.Join(projectRoot, "test", "test-updates", "branch-4", "1", "1674170952")
	branch := "DO_NOT_USE"
	runtimeVersion := "1"

	updateId1 := performUpload(t, projectRoot, branch, runtimeVersion, sampleUpdatePath)
	w := markUpdateAsUploaded(t, branch, runtimeVersion, updateId1)
	if w.Code != 200 {
		t.Fatalf("First mark as uploaded failed with status %d", w.Code)
	}

	updateId2 := performUpload(t, projectRoot, branch, runtimeVersion, sampleUpdatePath)
	w2 := markUpdateAsUploaded(t, branch, runtimeVersion, updateId2)
	if w2.Code == 200 {
		t.Fatalf("Second mark as uploaded should have failed (non-200), got %d", w2.Code)
	}
	// Should return first update ID
	lastUpdate, err := update.GetLatestUpdateBundlePathForRuntimeVersion(branch, runtimeVersion)
	if err != nil {
		t.Fatalf("Error getting latest update: %v", err)
	}
	assert.NotNil(t, lastUpdate, "Expected non-nil")
	assert.Equal(t, updateId1, lastUpdate.UpdateId, "Expected update ID to match")
}

func TestDifferentUpload(t *testing.T) {
	teardown := setup(t)
	defer teardown()
	mockExpoForRequestUploadUrlTest("staging")
	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("Error finding project root: %v", err)
	}
	sampleUpdatePath := filepath.Join(projectRoot, "test", "test-updates", "branch-4", "1", "1674170952")
	branch := "DO_NOT_USE"
	runtimeVersion := "1"
	updateId1 := performUpload(t, projectRoot, branch, runtimeVersion, sampleUpdatePath)
	fmt.Println(updateId1)
	w := markUpdateAsUploaded(t, branch, runtimeVersion, updateId1)
	if w.Code != 200 {
		t.Fatalf("First mark as uploaded failed with status %d", w.Code)
	}
	sampleOtherUpdatePath := filepath.Join(projectRoot, "test", "test-updates", "branch-4", "1", "1674170951")
	updateId2 := performUpload(t, projectRoot, branch, runtimeVersion, sampleOtherUpdatePath)
	fmt.Println(updateId2)
	w2 := markUpdateAsUploaded(t, branch, runtimeVersion, updateId2)
	assert.Equal(t, 200, w2.Code, "Expected status code 200")
	// Should return latest update ID
	lastUpdate, err := update.GetLatestUpdateBundlePathForRuntimeVersion(branch, runtimeVersion)
	if err != nil {
		t.Fatalf("Error getting latest update: %v", err)
	}
	assert.NotNil(t, lastUpdate, "Expected non-nil")
	assert.Equal(t, updateId2, lastUpdate.UpdateId, "Expected update ID to match")
}
