package main

import (
	"fmt"
	"net/http"
	"io"
	"encoding/base64"

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

	file, fileHeader, err := r.FormFile("thumbnail")
	if err != nil {
    respondWithError(w, http.StatusBadRequest, "Couldn't get data", err)
    return
	}
	defer file.Close()

	mediaType := fileHeader.Header.Get("Content-Type")
	if mediaType == "" {
    respondWithError(w, http.StatusBadRequest, "Missing Content-Type for thumbnail", nil)
    return
	}

	imageData, err := io.ReadAll(file)
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

	base64Encoded := base64.StdEncoding.EncodeToString(imageData)
	base64DataURL := fmt.Sprintf("data:%s;base64,%s", mediaType, base64Encoded)
	metadata.ThumbnailURL = &base64DataURL
	
	err = cfg.db.UpdateVideo(metadata)
	if err != nil {
    respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
    return
	}

	respondWithJSON(w, http.StatusOK, metadata)
}
