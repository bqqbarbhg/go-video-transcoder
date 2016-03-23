package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/bqqbarbhg/go-video-transcoder/ownedfile"
	"github.com/bqqbarbhg/go-video-transcoder/transcode"
	"github.com/bqqbarbhg/go-video-transcoder/workqueue"

	"github.com/gorilla/mux"
)

import _ "crypto/sha512"

var serveCollection *ownedfile.Collection = ownedfile.NewCollection()

var fastProcessQueue *workqueue.WorkQueue
var slowProcessQueue *workqueue.WorkQueue

var tempBase string
var serveBase string

var authUri string
var storageUri string
var apiUri string

var requestID int32

func logError(err error, context string, action string) {
	if err != nil {
		log.Printf("%s: %s failed: %s", context, action, err.Error())
	} else {
		log.Printf("%s: %s succeeded", context, action)
	}
}

func authenticate(r *http.Request) (user string, err error) {

	authorization := r.Header.Get("Authorization")
	upload_token := r.URL.Query().Get("upload_token")

	// Request GET userinfo-endpoint with the bearer token
	client := &http.Client{}
	req, err := http.NewRequest("GET", authUri, nil)
	if err != nil {
		return "", err
	}
	req.Header.Add("Accept", "application/json")
	if authorization != "" {
		req.Header.Add("Authorization", authorization)
	}
	if upload_token != "" {
		req.Header.Add("X-Upload-Token", upload_token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", errors.New("OIDC responded with non-200 status")
	}

	// Decode the response JSON
	decoder := json.NewDecoder(resp.Body)
	data := make(map[string]interface{})
	err = decoder.Decode(&data)
	if err != nil {
		return "", err
	}

	// Find the user id from the subject
	uid := data["sub"]
	strid, ok := uid.(string)
	if !ok {
		return "", errors.New("OIDC did not return an user id")
	}

	return strid, nil
}

func generateToken() (string, error) {
	length := 18

	buffer := make([]byte, length)
	_, err := rand.Read(buffer)
	if err != nil {
		return "", err
	}
	return "video-" + base64.URLEncoding.EncodeToString(buffer), nil
}

type videoToTranscode struct {
	dlPath    string
	srcPath   string
	dstPath   string
	servePath string
	url       string

	thumbDstPath   string
	thumbServePath string
	thumbUrl       string

	deleteUrl string

	owner string

	rotation int
}

func generateThumbnail(video *videoToTranscode, relativeTime float64) error {

	// Extract the duration
	duration, err := transcode.ExtractDuration(video.srcPath)
	if err != nil {
		return err
	}

	// Generate the thumbnail
	time := duration * relativeTime
	options := transcode.Options{
		CompensateRotation: video.rotation,
		Quality:            transcode.QualityHigh,
	}
	err = transcode.GenerateThumbnail(video.srcPath, video.thumbDstPath, time, &options)
	if err != nil {
		return err
	}

	// Move the generated thumbnail to the serve path
	err = serveCollection.Move(video.thumbDstPath, video.thumbServePath, video.owner)
	if err != nil {
		_ = os.Remove(video.thumbDstPath)
		return err
	}

	return nil
}

func transcodeVideo(video *videoToTranscode, quality transcode.Quality) error {

	// Do the transcoding itself
	options := transcode.Options{
		CompensateRotation: video.rotation,
		Quality:            quality,
	}
	err := transcode.TranscodeMP4(video.srcPath, video.dstPath, &options)
	if err != nil {
		return err
	}

	// Move the transcoded video to the serving path
	err = serveCollection.Move(video.dstPath, video.servePath, video.owner)
	if err != nil {
		_ = os.Remove(video.dstPath)
		return err
	}

	return nil
}

func processVideoFast(video *videoToTranscode) {

	// Extract the rotation from the metadata
	rotation, err := transcode.ExtractRotation(video.srcPath)
	logError(err, video.srcPath, "Extract rotation")
	if err == nil {
		video.rotation = rotation
	}

	// Generate a thumbnail for the video
	err = generateThumbnail(video, 0.3)
	logError(err, video.srcPath, "Generate thumbnail")

	// Transcode a quick, low quality version to make the service responsive
	err = transcodeVideo(video, transcode.QualityLow)
	logError(err, video.srcPath, "Transcode low-quality")

	// Queue the full quality transcoding
	slowProcessQueue.AddBlocking(func() {
		processVideoSlow(video)
	})
}

func processVideoSlow(video *videoToTranscode) {

	// Transcode a better quality version of the video
	err := transcodeVideo(video, transcode.QualityHigh)
	logError(err, video.srcPath, "Transcode high-quality")

	// Remove the source file as it's not needed anymore
	err = os.Remove(video.srcPath)
	logError(err, video.srcPath, "Delete source file")
}

func optionsHandler(methods ...string) func(http.ResponseWriter, *http.Request) (int, error) {
	return func(w http.ResponseWriter, r *http.Request) (int, error) {
		w.Header().Set("Access-Control-Allow-Methods", strings.Join(methods, ", "))
		w.WriteHeader(http.StatusOK)
		return http.StatusOK, nil
	}
}

func wrappedHandler(inner func(http.ResponseWriter, *http.Request) (int, error)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		reqID := atomic.AddInt32(&requestID, 1)
		log.Printf("request-%d: %s %s", reqID, r.Method, r.URL.Path)

		w.Header().Set("Access-Control-Allow-Origin", "*")

		// Delegate to the inner handler
		status, err := inner(w, r)

		if err != nil {
			log.Printf("request-%d: Failure %d: %s", reqID, status, err)

			w.WriteHeader(status)

			ret := struct {
				Error string `json:"error"`
			}{
				err.Error(),
			}
			err := json.NewEncoder(w).Encode(ret)
			if err != nil {
				log.Printf("request-%d: Failed to send error response: %s", reqID, err)
			}
		} else {
			log.Printf("request-%d: Success %d", reqID, status)
		}
	}
}

func authenticatedHandler(inner func(http.ResponseWriter, *http.Request, string) (int, error)) func(http.ResponseWriter, *http.Request) (int, error) {
	return func(w http.ResponseWriter, r *http.Request) (int, error) {

		// Authenticate
		user, err := authenticate(r)
		if err != nil {
			return http.StatusUnauthorized, err
		}

		// Delegate to the inner handler
		return inner(w, r, user)
	}
}

func createVideoToTranscode(token string, serveVideoPath string, serveThumbPath string, user string) *videoToTranscode {
	return &videoToTranscode{
		dlPath:    path.Join(tempBase, token+".dl.mp4"),
		srcPath:   path.Join(tempBase, token+".src.mp4"),
		dstPath:   path.Join(tempBase, token+".dst.mp4"),
		servePath: serveVideoPath,
		url:       fmt.Sprintf("%s/%s.mp4", storageUri, token),

		thumbDstPath:   path.Join(tempBase, token+".jpg"),
		thumbServePath: serveThumbPath,
		thumbUrl:       fmt.Sprintf("%s/%s.jpg", storageUri, token),

		deleteUrl: fmt.Sprintf("%s/uploads/%s", apiUri, token),

		owner: user,
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request, user string) (int, error) {

	var video *videoToTranscode

	// Generate an unique token and assign the file to the current user
	for try := 0; try < 10; try++ {
		token, err := generateToken()
		if err != nil {
			return http.StatusInternalServerError, err
		}

		serveVideoPath := path.Join(serveBase, token+".mp4")
		serveThumbPath := path.Join(serveBase, token+".jpg")

		// Reserve the owner for the destination files
		err = serveCollection.Create(serveVideoPath, user)
		if err != nil {
			log.Printf("Failed to create video: %s", err)
			continue
		}
		err = serveCollection.Create(serveThumbPath, user)
		if err != nil {
			log.Printf("Failed to create thumbnail: %s", err)
			err := serveCollection.Delete(serveVideoPath, user)
			if err != nil {
				log.Printf("Failed to remove video: %s", err)
			}
			continue
		}

		video = createVideoToTranscode(token, serveVideoPath, serveThumbPath, user)
		break
	}

	if video == nil {
		return http.StatusInternalServerError, errors.New("Could not create unique name")
	}

	log.Printf("%s: Created owned file", video.srcPath)

	// Download the resource data
	dlFile, err := os.Create(video.dlPath)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	defer dlFile.Close()

	title := ""

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/") {
		log.Printf("%s: Found multipart data", video.srcPath)
		reader, err := r.MultipartReader()
		if err != nil {
			return http.StatusBadRequest, err
		}

		didDownload := false

		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}

			if err != nil {
				return http.StatusBadRequest, err
			}

			if part.FormName() == "video" {
				log.Printf("%s: Downloading %s=%s (%s)", video.srcPath, part.FormName(), part.FileName(),
					part.Header.Get("Content-Type"))

				title = part.FileName()

				_, err = io.Copy(dlFile, part)
				if err != nil {
					return http.StatusInternalServerError, err
				}

				didDownload = true
			}

			part.Close()
		}

		if !didDownload {
			return http.StatusInternalServerError, errors.New("'video' not found in multipart data")
		}

	} else {
		log.Printf("%s: Downloading raw body data: %s", video.srcPath, contentType)

		_, err = io.Copy(dlFile, r.Body)
		if err != nil {
			return http.StatusInternalServerError, err
		}
	}

	log.Printf("%s: Downloaded video data", video.srcPath)

	err = os.Rename(video.dlPath, video.srcPath)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	// Process the video
	didAdd := fastProcessQueue.AddIfSpace(func() {
		processVideoFast(video)
	})

	// If there is no space in the work queue delete the temporary files
	if !didAdd {
		log.Printf("%s: Process queue full: cancelling processsing", video.srcPath)

		err := os.Remove(video.srcPath)
		logError(err, video.srcPath, "Delete source file")

		err = serveCollection.Delete(video.servePath, user)
		logError(err, video.srcPath, "Delete serve video file")

		err = serveCollection.Delete(video.thumbServePath, user)
		logError(err, video.srcPath, "Delete serve thumbnail file")

		return http.StatusServiceUnavailable, errors.New("Process queue full")
	}

	redirect := r.URL.Query().Get("redirect_to")

	if redirect != "" {
		redirectUrl, err := url.Parse(redirect)
		if err != nil {
			return http.StatusBadRequest, err
		}

		values := redirectUrl.Query()
		values.Add("video_url", video.url)
		values.Add("thumb_url", video.thumbUrl)
		values.Add("delete_url", video.deleteUrl)
		if title != "" {
			values.Add("title", title)
		}
		redirectUrl.RawQuery = values.Encode()

		http.Redirect(w, r, redirectUrl.String(), http.StatusFound)

		return http.StatusFound, nil
	} else {
		ret := struct {
			Video     string `json:"video"`
			Thumbnail string `json:"thumbnail"`
			DeleteUrl string `json:"deleteUrl"`
			Title     string `json:"title,omitempty"`
		}{
			video.url,
			video.thumbUrl,
			video.deleteUrl,
			title,
		}
		err = json.NewEncoder(w).Encode(ret)
		if err != nil {
			log.Printf("Failed to send response: %s", err.Error())
		}

		return http.StatusOK, nil
	}
}

func deleteHandler(w http.ResponseWriter, r *http.Request, user string) (int, error) {

	// Ignore the body
	_, err := io.Copy(ioutil.Discard, r.Body)
	if err != nil {
		return http.StatusBadRequest, err
	}

	vars := mux.Vars(r)
	token := vars["token"]

	// Delete the owned files
	serveVideoPath := path.Join(serveBase, token+".mp4")
	serveThumbPath := path.Join(serveBase, token+".jpg")

	videoErr := serveCollection.Delete(serveVideoPath, user)
	thumbErr := serveCollection.Delete(serveThumbPath, user)

	logError(videoErr, serveVideoPath, "Delete file")
	logError(thumbErr, serveThumbPath, "Delete file")

	if videoErr == nil && thumbErr == nil {
		w.WriteHeader(http.StatusNoContent)
		return http.StatusNoContent, nil
	}

	if ownedfile.IsPermissionDenied(videoErr) {
		return http.StatusForbidden, videoErr
	} else if ownedfile.IsPermissionDenied(thumbErr) {
		return http.StatusForbidden, thumbErr
	} else if videoErr != nil {
		return http.StatusInternalServerError, videoErr
	} else if thumbErr != nil {
		return http.StatusInternalServerError, thumbErr
	} else {
		return http.StatusInternalServerError, errors.New("Forbidden code path")
	}
}

func queuePendingVideosToTranscode() {

	files, err := ioutil.ReadDir(tempBase)
	if err != nil {
		log.Printf("Failed to search pending transcode work: %s", err.Error())
		return
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		p := file.Name()
		if !strings.HasSuffix(p, ".src.mp4") {
			continue
		}
		log.Printf("Found unprocessed video %s, preparing to transcode", p)

		parts := strings.Split(p, "/")
		if len(parts) == 0 {
			log.Printf("%s: Failed to extract token", p)
			continue
		}

		token := strings.TrimSuffix(parts[len(parts)-1], ".src.mp4")

		serveVideoPath := path.Join(serveBase, token+".mp4")
		serveThumbPath := path.Join(serveBase, token+".jpg")

		videoOwner, err := serveCollection.ReadOwner(serveVideoPath)
		if err != nil {
			log.Printf("%s: Failed to read video owner", p)
			continue
		}

		thumbOwner, err := serveCollection.ReadOwner(serveThumbPath)
		if err != nil {
			log.Printf("%s: Failed to read thumbnail owner", p)
			continue
		}

		if videoOwner != thumbOwner {
			log.Printf("%s: Owner mismatch", p)
			continue
		}

		video := createVideoToTranscode(token, serveVideoPath, serveThumbPath, videoOwner)

		didAdd := fastProcessQueue.AddIfSpace(func() {
			processVideoFast(video)
		})

		if !didAdd {
			log.Printf("%s: Process queue full: skipped", video.srcPath)
		} else {
			log.Printf("%s: Added to process queue", video.srcPath)
		}
	}
}

func main() {

	// Resolve URLs from environment variables
	//
	// Common:
	//   GOTR_TEMP_PATH: Path to download and process videos in
	//   GOTR_SERVE_PATH: Path to copy transcoded videos _needs_ to be in the same mount as GOTR_TEMP_PATH
	//                    since the processed videos are renamed to here when done.
	//   GOTR_STORAGE_URL_PATH: Base path appeneded to GOTR_URI or LAYERS_API_URI that serves files from GOTR_SERVE_PATH
	//   GOTR_API_URL_PATH: Base path appended to GOTR_UR or LAYERS_API_URI that is used for the API calls
	//
	// Layers Box:
	//   LAYERS_API_URI: URL of the box (should be predefined by Layers Box)
	//   AUTH_URL_PATH: Path appended to LAYERS_API_URI for the authentication /userinfo endpoint
	//
	// Standalone:
	//   GOTR_URI: URL of this server
	//   AUTH_URI: URL of the authentication /userinfo endpoint
	//
	// Optional:
	//   GOTR_FAST_TRANSCODE_THREADS: Number of workers that do fast low latency work (default 4)
	//   GOTR_SLOW_TRANSCODE_THREADS: Number of workerst that do slow, but higher quality work (default 1)

	layersApiUri := strings.TrimSuffix(os.Getenv("LAYERS_API_URI"), "/")

	appUri := strings.TrimSuffix(os.Getenv("GOTR_URI"), "/")
	if appUri == "" {
		appUri = layersApiUri
	}

	authUri = strings.TrimSuffix(os.Getenv("AUTH_URI"), "/")
	if authUri == "" {
		if layersApiUri != "" {
			authPath := strings.Trim(os.Getenv("AUTH_URL_PATH"), "/")
			if authPath != "" {
				authUri = layersApiUri + "/" + authPath
			} else {
				authUri = layersApiUri + "/o/oauth2/userinfo"
			}
		}
	}

	if appUri == "" {
		log.Printf("No app URI found, use LAYERS_API_URI or GOTR_URI to specify")
		os.Exit(11)
	}

	if authUri == "" {
		log.Printf("No auth URI found, specify AUTH_URI or AUTH_URL_PATH")
		os.Exit(11)
	}

	if os.Getenv("GOTR_STORAGE_URL_PATH") == "" {
		log.Printf("No storage path found, specify GOTR_STORAGE_URL_PATH use '/' if root")
		os.Exit(11)
	}

	if os.Getenv("GOTR_API_URL_PATH") == "" {
		log.Printf("No API path found, specify GOTR_API_URL_PATH use '/' if root")
		os.Exit(11)
	}

	numFastTranscodeThreads := 4
	numSlowTranscodeThreads := 1

	if os.Getenv("GOTR_FAST_TRANSCODE_THREADS") != "" {
		var err error
		numFastTranscodeThreads, err = strconv.Atoi(os.Getenv("GOTR_FAST_TRANSCODE_THREADS"))
		if err != nil {
			log.Printf("Expected a number for GOTR_FAST_TRANSCODE_THREADS")
			os.Exit(11)
		}
	}
	if os.Getenv("GOTR_SLOW_TRANSCODE_THREADS") != "" {
		var err error
		numSlowTranscodeThreads, err = strconv.Atoi(os.Getenv("GOTR_SLOW_TRANSCODE_THREADS"))
		if err != nil {
			log.Printf("Expected a number for GOTR_SLOW_TRANSCODE_THREADS")
			os.Exit(11)
		}
	}

	storageUri = strings.TrimSuffix(appUri+os.Getenv("GOTR_STORAGE_URL_PATH"), "/")
	apiUri = strings.TrimSuffix(appUri+os.Getenv("GOTR_API_URL_PATH"), "/")
	tempBase = os.Getenv("GOTR_TEMP_PATH")
	serveBase = os.Getenv("GOTR_SERVE_PATH")

	fastProcessQueue = workqueue.New(numFastTranscodeThreads)
	slowProcessQueue = workqueue.New(numSlowTranscodeThreads)

	if tempBase == "" {
		log.Printf("No temp folder found, specify GOTR_TEMP_PATH")
		os.Exit(11)
	}

	if serveBase == "" {
		log.Printf("No serve folder found, specify GOTR_SERVE_PATH")
		os.Exit(11)
	}

	log.Printf("Configuration successful")
	log.Printf("  %12s: %s", "Auth URI", authUri)
	log.Printf("  %12s: %s/", "API URI", apiUri)
	log.Printf("  %12s: %s/", "Serve URI", storageUri)
	log.Printf("  %12s: %s", "Temp path", tempBase)
	log.Printf("  %12s: %s", "Serve path", serveBase)
	log.Printf("  %12s: %d fast, %d slow", "Threads", numFastTranscodeThreads, numSlowTranscodeThreads)

	// If there is pending work to do add it to the work queue
	log.Printf("Searching for pending work")
	queuePendingVideosToTranscode()

	// Setup the router and start serving
	r := mux.NewRouter()

	r.HandleFunc("/uploads", wrappedHandler(authenticatedHandler(uploadHandler))).Methods("POST")
	r.HandleFunc("/uploads/{token}", wrappedHandler(authenticatedHandler(deleteHandler))).Methods("DELETE")

	r.HandleFunc("/uploads", wrappedHandler(optionsHandler("POST"))).Methods("OPTIONS")
	r.HandleFunc("/uploads/{token}", wrappedHandler(optionsHandler("DELETE"))).Methods("DELETE")

	port := ":8080"

	log.Printf("Serving at %s", port)
	err := http.ListenAndServe(port, r)
	if err != nil {
		log.Printf("Failed to start server: %s", err.Error())
		os.Exit(10)
	}
}
