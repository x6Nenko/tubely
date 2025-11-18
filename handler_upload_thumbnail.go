package main

import (
	"fmt"
	"net/http"
	"io"
	"path/filepath"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"os"
	"mime"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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


	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	const maxMemory = 10 << 20  // 10 * 2^20 = 10 * 1024 * 1024 = 10MB
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
    respondWithError(w, http.StatusBadRequest, "Couldn't parse data", err)
    return
	}

	uploadedFile, fileHeader, err := r.FormFile("thumbnail")
	if err != nil {
    respondWithError(w, http.StatusBadRequest, "Couldn't get data", err)
    return
	}
	defer uploadedFile.Close()

	rawData := fileHeader.Header.Get("Content-Type")
	if rawData == "" {
    respondWithError(w, http.StatusBadRequest, "Missing Content-Type for thumbnail", nil)
    return
	}

	mediaType, _, err := mime.ParseMediaType(rawData)
	if err != nil {
    respondWithError(w, http.StatusBadRequest, "Couldn't parse data", err)
    return
	}

	if mediaType != "image/jpeg" && mediaType != "image/png" {
    respondWithError(w, http.StatusBadRequest, "Wrong file type", err)
    return
	}

	// From the strings package - splitting by delimiter
	mediaTypeParts := strings.Split(mediaType, "/")
	// parts = ["image", "png"]

	// Make a 32-byte slice
	randomBytes := make([]byte, 32)

	// Fill with random data
	_, err = rand.Read(randomBytes)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't generate random bytes", err)
    return
	}

	// Encode to base64 string
	randomString := base64.RawURLEncoding.EncodeToString(randomBytes)

	// Create filename with extension
	filename := fmt.Sprintf("%s.%s", randomString, mediaTypeParts[1])

	fullPath := filepath.Join(cfg.assetsRoot, filename)

	diskFile, err := os.Create(fullPath)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't create file", nil)
    return
	}
	defer diskFile.Close()

	_, err = io.Copy(diskFile, uploadedFile)
	if err != nil {
    respondWithError(w, http.StatusInternalServerError, "Couldn't get data", err)
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

	url := fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, filename)
	metadata.ThumbnailURL = &url

	err = cfg.db.UpdateVideo(metadata)
	if err != nil {
    respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
    return
	}

	respondWithJSON(w, http.StatusOK, metadata)
}
