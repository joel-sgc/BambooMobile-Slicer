# Bamboo Mobile Slicer API

A lightweight Go server that acts as a headless slicing backend for the **Bamboo Mobile** Tauri app. It intercepts MakerWorld `.3mf` files, compiles them using a headless instance of OrcaSlicer, and streams the printable `.gcode.3mf` binary back to your mobile device over the local network.

## 🏗 How It Works (The Companion Architecture)

Bamboo Mobile is designed to bypass the cloud and keep print dispatching strictly local. Because compiling G-code requires heavy graphical libraries and significant memory, your Android phone offloads the heavy math to this Go server.

1. **Intercept:** The Bamboo Mobile app catches a `bambustudioopen://` deep link from MakerWorld.
2. **Dispatch:** The mobile app extracts the download URL and sends a POST request to this Go API.
3. **Download & Slice:** This server downloads the `.3mf` and executes a background Docker container to headlessly slice the geometry via a virtual Wayland display.
4. **Stream:** The Go server streams the compiled `.gcode.3mf` binary back to the Android app.
5. **Print:** Bamboo Mobile transfers the compiled file directly to your Bambu Lab printer's SD card via local FTP and triggers the print via MQTT.

---

## ⚙️ Prerequisites

- **Host OS:** A Linux host (Standard x86_64 VPS/NAS, or an ARM64 board like a Raspberry Pi).
- **Go:** v1.20+ installed on the host.
- **Docker & Docker Compose:** To run and manage the isolated OrcaSlicer daemon.

---

## 🚀 Setup & Installation

We use a Docker container to sandbox the heavy GTK/Wayland graphical dependencies required by the slicer engine. Instead of spinning up a container for every request, we run it as a persistent background daemon via Docker Compose to keep execution nearly instantaneous.

First, create the shared directory where Go and Docker will pass files back and forth:

```
mkdir -p /tmp/slicer_data
sudo chmod 777 /tmp/slicer_data
```

### Step 1: Start the Docker Daemon via Compose

Create a file named `docker-compose.yml` in your project root directory and paste the following configuration. If you are running on an ARM64 architecture (like a Raspberry Pi 4 or 5), swap the commented `image` tags as indicated:

```
services:
  orcaslicer-daemon:
    # For Raspberry Pi / ARM64, comment out the linuxserver image and uncomment the matszwe02 image
    # image: lscr.io/linuxserver/orcaslicer:latest
    image: matszwe02/orcaslicer-arm:latest
    container_name: orcaslicer-daemon
    environment:
      - PUID=911
      - PGID=911
      - TZ=America/Chicago
    volumes:
      - ~/.orcaslicer_docker:/config
      - /tmp/slicer_data:/config/workspace
    ports:
      - 3000:3000
    restart: unless-stopped
```

Launch the background daemon using:

```
docker compose up -d
```

> **⚠️ Raspberry Pi Hardware Warning:** Slicing 3D geometry is incredibly RAM-intensive. A Raspberry Pi 4 or 5 is required. If your Pi has **less than 8GB of RAM**, you MUST configure a Linux swapfile of at least 4GB. If the container runs out of physical memory during a complex multi-color slice, the OS will instantly OOM-kill the container, causing the Go API to crash.

### Step 2: Compile the Go Binary

Instead of running raw source code in production, compile the application into a standalone static binary. Run this from inside your repository folder:

```
go build -o slicer-x86_64 .
```

or alternatively to compile for an ARM based system:

```
GOOS=linux GOARCH=arm64 go build -o slicer-arm64 .
```

### Step 3: Configure systemd (Systemctl Service)

To ensure the server automatically boots on system startup and restarts itself if it ever encounters a fatal error, you can configure it as a background system service.

Create a new service configuration file:

```
sudo nano /etc/systemd/system/BambooMobile-Slicer.service
```

Paste the following block into the file. _Make sure to update the `User`, `WorkingDirectory`, and executable path to match your environment:_

```
[Unit]
Description=Bamboo Mobile Headless Slicer API
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
User=YOUR_LINUX_USER
WorkingDirectory=/PATH/TO/BambooMobile-Slicer
ExecStart=/PATH/TO/BambooMobile-Slicer/slicer-x86_64
# ExecStart=/PATH/TO/BambooMobile-Slicer/slicer-arm64
Environment="PORT=8080"
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Save and exit (`Ctrl+O`, `Enter`, `Ctrl+X`).

### Step 4: Enable and Start the Service

Reload the systemd manager configuration, enable the service to launch on startup, and trigger it immediately:

```
sudo systemctl daemon-reload
sudo systemctl enable --now BambooMobile-Slicer
```

To track the live output logs of the server as jobs are processed:

```
journalctl -u BambooMobile-Slicer -f
```

---

## 🛠 Configuration

### Custom Ports

You can change the port the server listens on by modifying the `Environment="PORT=8080"` line inside your `/etc/systemd/system/BambooMobile-Slicer.service` file. Run `sudo systemctl daemon-reload && sudo systemctl restart BambooMobile-Slicer` to apply changes.

### Forcing Printer Profiles (Optional)

If you want to ensure the server always slices for your specific printer setup (e.g., forcing a P1S profile) regardless of what the MakerWorld creator used:

1. Open OrcaSlicer on your desktop computer.
2. Export your configuration bundle (`File > Export > Export Config...`).
3. Save it as `p1s_profile.json` and place it in the `/tmp/slicer_data` folder on your server.
4. Add `--load-settings /config/workspace/p1s_profile.json` to the `docker exec` command block in your `main.go`.

---

## 📡 API Reference

### `POST /api/slice`

Instructs the server to download and compile a MakerWorld file.

**Request Body (JSON):**

```
{
  "url": "https://makerworld.com/path/to/download.3mf"
}
```

**Response:**

- **Content-Type:** `application/octet-stream`
- **Body:** The raw binary stream of the compiled `.gcode.3mf` file, ready to be sent to the printer via FTP.

---

## 🛟 Troubleshooting

- **`exit status 127`**: The Go server can't find the `orcaslicer` executable inside the container. Verify the binary path using `docker exec orcaslicer-daemon find / -name "*orcaslicer*" -type f -executable`.
- **`exit status 243`**: The slicer compiled the file but failed to write it to disk. Ensure `/tmp/slicer_data` has `777` permissions so the container's user can write back to the host volume.
- **`exit status 253`**: The Go server downloaded the file, but Docker can't see it. Ensure the `SharedDir` variable in `main.go` perfectly matches the `-v` volume mount you used inside the `docker-compose.yml`.
