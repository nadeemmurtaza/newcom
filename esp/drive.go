package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Phase 24: Cloud Storage (Google Drive clone)
//
// Phase 61: org_id and owner_id used to come straight from form fields --
// any caller could upload a file into any org, attributed to any user,
// with no check at all. Now requires a verified session (userAuthMiddleware)
// and the caller must actually belong to the org they're uploading into;
// owner_id is the authenticated user, never a value the client supplies.
func handleDriveUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseMultipartForm(50 << 20) // 50 MB max in memory
	if err != nil {
		http.Error(w, "File too large", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	orgID := r.FormValue("org_id")
	ownerID := r.Header.Get("X-User-ID") // set by userAuthMiddleware from a verified session
	if orgID == "" || ownerID == "" {
		http.Error(w, "org_id is required", http.StatusBadRequest)
		return
	}
	if !hasPermission(ownerID, orgID, "drive:write") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Store locally as a simulation of S3
	os.MkdirAll("./cloud_storage", 0755)
	filePath := filepath.Join("./cloud_storage", header.Filename)
	
	out, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	defer out.Close()
	io.Copy(out, file)

	// Save to DB
	var fileID string
	err = db.QueryRow(`
		INSERT INTO drive_files (org_id, owner_id, filename, size_bytes, mime_type, storage_url) 
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		orgID, ownerID, header.Filename, header.Size, header.Header.Get("Content-Type"), "/cloud_storage/"+header.Filename,
	).Scan(&fileID)
	
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": fileID, "url": "/cloud_storage/" + header.Filename})
}
