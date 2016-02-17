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
	"os"
	"path"
	"strings"
	"sync/atomic"

	"github.com/bqqbarbhg/go-video-transcoder/ownedfile"
	"github.com/bqqbarbhg/go-video-transcoder/transcode"
	"github.com/bqqbarbhg/go-video-transcoder/workqueue"

	"github.com/gorilla/mux"
)

var serveCollection *ownedfile.Collection = ownedfile.NewCollection()

var fastProcessQueue = workqueue.New(4)
var slowProcessQueue = workqueue.New(1)

var tempBase string
var serveBase string

var layersApiUri string
var storageUri string

var requestID int32

func logError(err error, context string, action string) {
	if err != nil {
		log.Printf("%s: %s failed: %s", context, action, err.Error())
	} else {
		log.Printf("%s: %s succeeded", context, action)
	}
}

func authenticate(r *http.Request) (user string, err error) {

	// Request GET /o/auth2/userinfo with the bearer token
	client := &http.Client{}
	url := layersApiUri + "/o/oauth2/userinfo"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Add("Authorization", r.Header.Get("Authorization"))
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
	return base64.URLEncoding.EncodeToString(buffer), nil
}

type videoToTranscode struct {
	srcPath   string
	dstPath   string
	servePath string
	url       string

	thumbDstPath   string
	thumbServePath string
	thumbUrl       string

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

func wrappedHandler(inner func(http.ResponseWriter, *http.Request) (int, error)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		reqID := atomic.AddInt32(&requestID, 1)
		log.Printf("request-%d: %s %s", reqID, r.Method, r.URL.Path)

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

		video = &videoToTranscode{
			srcPath:   path.Join(tempBase, token+".src.mp4"),
			dstPath:   path.Join(tempBase, token+".dst.mp4"),
			servePath: serveVideoPath,
			url:       fmt.Sprintf("%s/%s.mp4", storageUri, token),

			thumbDstPath:   path.Join(tempBase, token+".jpg"),
			thumbServePath: serveThumbPath,
			thumbUrl:       fmt.Sprintf("%s/%s.jpg", storageUri, token),

			owner: user,
		}
		break
	}

	if video == nil {
		return http.StatusInternalServerError, errors.New("Could not create unique name")
	}

	log.Printf("%s: Created owned file", video.srcPath)

	// Download the resource data
	srcFile, err := os.Create(video.srcPath)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	_, err = io.Copy(srcFile, r.Body)
	if err != nil {
		srcFile.Close()
		return http.StatusInternalServerError, err
	}
	srcFile.Close()

	log.Printf("%s: Downloaded video data", video.srcPath)

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

	ret := struct {
		Video     string `json:"video"`
		Thumbnail string `json:"thumbnail"`
	}{
		video.url,
		video.thumbUrl,
	}
	err = json.NewEncoder(w).Encode(ret)
	if err != nil {
		log.Printf("Failed to send response: %s", err.Error())
	}
	return http.StatusOK, nil
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

func main() {
	layersApiUri = strings.TrimSuffix(os.Getenv("LAYERS_API_URI"), "/")
	storageUri = strings.TrimSuffix(layersApiUri+os.Getenv("GOTR_STORAGE_URL_PATH"), "/")
	tempBase = os.Getenv("GOTR_TEMP_PATH")
	serveBase = os.Getenv("GOTR_SERVE_PATH")

	r := mux.NewRouter()

	r.HandleFunc("/uploads", wrappedHandler(authenticatedHandler(uploadHandler))).Methods("POST")
	r.HandleFunc("/uploads/{token}", wrappedHandler(authenticatedHandler(deleteHandler))).Methods("DELETE")

	http.ListenAndServe(":8080", r)
}
