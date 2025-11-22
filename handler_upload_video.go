package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"crypto/rand"
	"net/http"
	"context"
	"mime"
	"fmt"
	"os"
	"os/exec"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1 << 30)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	metadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get metadata", err)
		return
	}

	if metadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	uploadedFile, fileHeader, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't get data", err)
		return
	}
	defer uploadedFile.Close()

	rawData := fileHeader.Header.Get("Content-Type")
	if rawData == "" {
		respondWithError(w, http.StatusBadRequest, "Missing Content-Type for video", nil)
		return
	}

	mediaType, _, err := mime.ParseMediaType(rawData)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse data", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Wrong file type", err)
		return
	}

	// Create temp file
	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't create temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())  // Delete file when done
	defer tempFile.Close()            // Close file when done (runs BEFORE remove)

	// Copy data from somewhere to temp file
	_, err = io.Copy(tempFile, uploadedFile)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't copy video data to temp file", err)
		return
	}

	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't reset the file pointer", err)
		return
	}

	// Generate random filename
	randomBytes := make([]byte, 32)
	_, err = rand.Read(randomBytes)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't generate random bytes struct", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't get aspect ratio", err)
		return
	}
	randomName := base64.RawURLEncoding.EncodeToString(randomBytes)
	fileKey := fmt.Sprintf("%s/%s.mp4", aspectRatio, randomName)

	processedOutputPath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't get processedOutputPath", err)
		return
	}

	// Open processed temp file
	processedTempFile, err := os.Open(processedOutputPath)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't create processed temp file", err)
		return
	}
	defer os.Remove(processedTempFile.Name())  // Delete file when done
	defer processedTempFile.Close()

	// Upload to S3
	_, err = cfg.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(fileKey),
		Body:        processedTempFile,  // This is an io.Reader!
		ContentType: aws.String("video/mp4"),
	})
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't upload to S3", err)
		return
	}

	video_url := fmt.Sprintf("%s,%s", cfg.s3Bucket, fileKey)
	metadata.VideoURL = &video_url

	err = cfg.db.UpdateVideo(metadata)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	// Convert to signed video before responding
	signedVideo, err := cfg.dbVideoToSignedVideo(metadata)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't convert to signed video before responding", err)
		return
	}

	respondWithJSON(w, http.StatusOK, signedVideo)
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	// Step 1: Split video.VideoURL on comma
	// But first, check if VideoURL is nil!
	if video.VideoURL == nil {
		return video, nil
	}
	
	// Get the actual string from the pointer
	videoURLString := *video.VideoURL
	
	// Split it
	parts := strings.Split(videoURLString, ",")
	
	// Step 2: Extract bucket and key
	bucket := parts[0]
	key := parts[1]
	
	// Step 3: Call generatePresignedURL
	presignedURL, err := generatePresignedURL(cfg.s3Client, bucket, key, time.Hour * 1)
	if err != nil {
		return database.Video{}, err
	}
	
	// Step 4: Update the video's VideoURL field
	video.VideoURL = &presignedURL
	
	// Step 5: Return the updated video
	return video, nil
}
  
func getVideoAspectRatio(filePath string) (string, error) {
	// Create a buffer
	var buffer bytes.Buffer

	// Create a command
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	// capture output by setting where it should write to
	cmd.Stdout = &buffer

	// Run it
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	bytesData := buffer.Bytes()  // returns []byte

	type stream struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}

	type ffprobe struct {
    Streams []stream `json:"streams"`
	}

	var data ffprobe
	err = json.Unmarshal(bytesData, &data)
	if err != nil {
		return "", err
	}

	firstStream := data.Streams[0]

	ratio := float64(firstStream.Width) / float64(firstStream.Height)
	targetRatio169 := 16.0 / 9.0
	targetRatio916 := 9.0 / 16.0
	tolerance := 0.1

	if ratio >= targetRatio169 - tolerance && ratio <= targetRatio169 + tolerance {
		return "landscape", nil // 16:9
	} else if ratio >= targetRatio916 - tolerance && ratio <= targetRatio916 + tolerance {
    return "portrait", nil // 9:16
	} else {
		return "other", nil
	}
}

func processVideoForFastStart(filePath string) (string, error) {
	outputPath := filePath + ".processing"

	// Create a command
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputPath)

	// Run it
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	return outputPath, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
 // Step 1: Create presign client
	presignClient := s3.NewPresignClient(s3Client)
	
	// Step 2: Call PresignGetObject
	presignedReq, err := presignClient.PresignGetObject(
		context.TODO(),
		&s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		},
		s3.WithPresignExpires(expireTime),
	)
	
	// Step 3: Error handling
	if err != nil {
		return "", err
	}
	
	// Step 4: Return the URL field
	return presignedReq.URL, nil
}