package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Define the incoming JSON payload from your Tauri app
type SliceRequest struct {
	DownloadURL string `json:"url"`
}

// Ensure the shared Docker volume path matches your setup
const SharedDir = "/tmp/slicer_data"

func downloadFile(url string, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	// Use 'dest' exactly as it was passed in!
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// executeSlicer triggers the background Docker container
func executeSlicer(inputFile string, outputFile string) error {
	containerInput := fmt.Sprintf("/config/workspace/%s", inputFile)
	containerOutput := fmt.Sprintf("/config/workspace/%s", outputFile)

	cmd := exec.Command("docker", "exec",
		"-u", "abc",
		"-e", "WAYLAND_DISPLAY=wayland-1",
		"-e", "XDG_RUNTIME_DIR=/config/.XDG",
		"orcaslicer-daemon",
		"/opt/orcaslicer/bin/orca-slicer",
		"--slice", "0",
		"--export-3mf", containerOutput,
		containerInput)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v - Logs: %s", err, stderr.String())
	}
	return nil
}

func handleSlice(w http.ResponseWriter, r *http.Request) {
	// 1. Only allow POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 2. Parse the MakerWorld URL from the JSON body
	var req SliceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Generate unique filenames using a timestamp to prevent overlapping requests
	timestamp := time.Now().UnixNano()
	inputFilename := fmt.Sprintf("job_%d.3mf", timestamp)
	outputFilename := fmt.Sprintf("job_%d.gcode.3mf", timestamp)

	inputPath := filepath.Join(SharedDir, inputFilename)
	outputPath := filepath.Join(SharedDir, outputFilename)

	// GARBAGE COLLECTION: Ensure files are deleted when the handler exits
	defer os.Remove(inputPath)
	defer os.Remove(outputPath)

	// 3. Download the file from MakerWorld
	fmt.Printf("Downloading: %s\n", req.DownloadURL)
	if err := downloadFile(req.DownloadURL, inputPath); err != nil {
		http.Error(w, fmt.Sprintf("Failed to download file: %v", err), http.StatusInternalServerError)
		return
	}

	// 4. Execute the headless Docker slicer
	fmt.Printf("Slicing job: %s\n", inputFilename)
	if err := executeSlicer(inputFilename, outputFilename); err != nil {
		http.Error(w, fmt.Sprintf("Slicing failed: %v", err), http.StatusInternalServerError)
		return
	}

	// 5. Stream the resulting file back to the phone
	fmt.Printf("Streaming result back to client...\n")
	streamFileToClient(w, outputPath)
}

// streamFileToClient sets the correct headers and writes the binary to the response
func streamFileToClient(w http.ResponseWriter, filePath string) {
	file, err := os.Open(filePath)
	if err != nil {
		http.Error(w, "Compiled file not found", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Tell the Tauri app this is a binary file download
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="print_job.gcode.3mf"`)

	io.Copy(w, file)}

func main() {
	// Ensure the shared directory exists
	os.MkdirAll(SharedDir, 0777)

	// Check for a PORT environment variable, default to 8080 if not found
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/api/slice", handleSlice)

	// Format the address string dynamically
	address := fmt.Sprintf("0.0.0.0:%s", port)
	
	fmt.Printf("Slicer API running on http://%s", address)
	
	err := http.ListenAndServe(address, nil)
	if err != nil {
		fmt.Printf("Server crashed: %v\n", err)
	}
}

