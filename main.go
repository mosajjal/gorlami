// sandbox-request is a web interface for requesting a browser sandbox.
// the application authenticates with oAtuh2, then presents a form to the user
// to choose the browser, session length, and whether or not the session is private.
// it then creates a headless container with the browser, using `accetto/ubuntu-vnc-xfce-chromium-g3:latest`
// it then registers a new path using a uuid inside the web app, and maps the noVNC port from the
// container to the web app, the user is presented with the generated URI, and the URI can be shared
// with other authenticated users of the web app if the session is not private. otherwise, only the
// user who requested the session can access it.
// after the session length expires, the container is destroyed.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"

	docker "github.com/fsouza/go-dockerclient"
	"github.com/gofiber/contrib/fiberzerolog"
	"github.com/gofiber/fiber/v2"
	"github.com/markbates/goth/providers/azuread"
	"github.com/rs/zerolog"

	"github.com/gofiber/fiber/v2/middleware/basicauth"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/gofiber/websocket/v2"
)

type Container struct {
	// container id
	ID            string
	Endpoint      string
	EndpointType  string
	IsEndpointTLS bool
}

//go:embed noVNC/*
var noVNC embed.FS

//go:embed kasmVNC
var kasmVNC embed.FS

//go:embed index.html
var indexHTML string

var nocolorLog = strings.ToLower(os.Getenv("NO_COLOR")) == "true"
var logger = zerolog.New(os.Stderr).With().Timestamp().Logger().Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339, NoColor: nocolorLog})

// GetFreePort gets a random open port
func GetFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return GetFreePort()
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (c *Config) NewContainer(containerTime time.Duration, service string) (*Container, error) {
	randomPort, err := GetFreePort()
	if err != nil {
		return nil, err
	}

	client, err := docker.NewClientFromEnv()
	if err != nil {
		return nil, err
	}

	serviceConfig := c.Services[service]

	// pull image
	err = client.PullImage(docker.PullImageOptions{
		Repository: serviceConfig.DockerImage,
	}, docker.AuthConfiguration{})
	if err != nil {
		return nil, err
	}
	// create container
	container, err := client.CreateContainer(docker.CreateContainerOptions{
		Config: &docker.Config{
			Image: serviceConfig.DockerImage,
			Tty:   true,
			ExposedPorts: map[docker.Port]struct{}{
				docker.Port(fmt.Sprintf("%s/tcp", serviceConfig.DockerPort)): {},
			},
			Env:       serviceConfig.Env,
			PortSpecs: []string{serviceConfig.DockerPort},
			Entrypoint: append([]string{
				"timeout",
				fmt.Sprintf("%d", int64(containerTime.Seconds())),
			}, serviceConfig.Entrypoint...),
		},
		HostConfig: &docker.HostConfig{
			AutoRemove: true,
			PortBindings: map[docker.Port][]docker.PortBinding{
				docker.Port(fmt.Sprintf("%s/tcp", serviceConfig.DockerPort)): {
					{
						HostIP:   "127.0.0.1",
						HostPort: fmt.Sprintf("%d", randomPort),
					},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	// give timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(10))
	defer cancel()
	// start container with a random port exposed to 6901
	err = client.StartContainerWithContext(container.ID, &docker.HostConfig{
		AutoRemove: true,
		PortBindings: map[docker.Port][]docker.PortBinding{
			docker.Port(fmt.Sprintf("%s/tcp", serviceConfig.DockerPort)): {
				{
					HostIP:   "127.0.0.1",
					HostPort: fmt.Sprintf("%d", randomPort),
				},
			},
		},
	}, ctx)
	if err != nil {
		return nil, err
	}
	// return container
	return &Container{
		ID:            container.ID,
		Endpoint:      fmt.Sprintf("127.0.0.1:%d", randomPort),
		EndpointType:  serviceConfig.DockerPortType,
		IsEndpointTLS: serviceConfig.DockerPortIsTLS,
	}, nil
}

// the key is the container ID
var RunningContainers = make(map[string]*Container)

func keys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func main() {
	// flag to read the config file path
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()
	config := readConfig(*configPath)

	app := fiber.New()

	// Logging Request ID
	app.Use(requestid.New())
	app.Use(fiberzerolog.New(fiberzerolog.Config{
		Logger: &logger,
		// For more options, see the Config section
		Fields: []string{fiberzerolog.FieldRequestID, fiberzerolog.FieldIP,
			fiberzerolog.FieldIPs, fiberzerolog.FieldLatency, fiberzerolog.FieldStatus,
			fiberzerolog.FieldMethod, fiberzerolog.FieldURL, fiberzerolog.FieldError},
	}))

	if config.Webserver.AuthProvider == "basic" {
		// add basic auth
		// Provide a minimal config
		app.Use(basicauth.New(basicauth.Config{
			Users: config.Webserver.Users,
		}))
	}
	if config.Webserver.AuthProvider == "azuread" {
		azuread.New(config.Webserver.AzureADKey, config.Webserver.AzureADSecret, config.Webserver.AzureADCallback, nil)
	}

	// an index page with a form to request a container
	app.Get("/", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/html")
		template := template.Must(template.New("index").Parse(indexHTML))
		template.Execute(c, map[string]any{
			"DefaultTimeout": config.Webserver.TimeoutDefault,
			"MaxTimeout":     config.Webserver.TimeoutMax,
			"Services":       keys(config.Services),
		})
		return nil
	})

	// request container POST request
	app.Post("/new_container", func(c *fiber.Ctx) error {
		// get the timeout from the request
		timeout := c.FormValue("timeout")
		// parse the timeout
		timeoutDuration, err := time.ParseDuration(timeout)
		if err != nil {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid timeout",
			})
		}
		// check if the timeout is within the limits
		if timeoutDuration < config.Webserver.TimeoutDefault || timeoutDuration > config.Webserver.TimeoutMax {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{
				"error": "timeout out of range",
			})
		}

		service := c.FormValue("service")
		// get the service from the config
		_, ok := config.Services[service]
		if !ok {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid service",
			})
		}

		// create container
		container, err := config.NewContainer(timeoutDuration, service)
		if err != nil {
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		}
		// add to running containers
		RunningContainers[container.ID] = container
		websockifyURI := fmt.Sprintf("view/%s/websockify", container.ID)
		time.Sleep(time.Second * 5) //TODO: how do we make this shorter?

		if container.EndpointType == "novnc" {
			return c.Redirect(fmt.Sprintf("/novnc/vnc.html?path=%s&password=headless", websockifyURI), http.StatusMovedPermanently)
		}
		if container.EndpointType == "kasm" {
			return c.Redirect(fmt.Sprintf("/kasm/index.html?path=%s", websockifyURI), http.StatusMovedPermanently)
		}
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "invalid endpoint type",
		})
	})

	// Get the subdirectory /static from the embedded filesystem
	noVNCSubFolder, err := fs.Sub(noVNC, "noVNC")
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to get subfolder")
	}
	app.Use("/novnc", filesystem.New(filesystem.Config{
		Root:   http.FS(noVNCSubFolder),
		Browse: false,
	}))

	// do the same for KASM
	kasmVNCSubFolder, err := fs.Sub(kasmVNC, "kasmVNC")
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to get subfolder")
	}
	app.Use("/kasm", filesystem.New(filesystem.Config{
		Root:   http.FS(kasmVNCSubFolder),
		Browse: false,
	}))

	// register the viewer handler with the sha256 of the container
	app.Get("/view/:id/websockify", func(c *fiber.Ctx) error {
		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}
		return websocket.New(func(c *websocket.Conn) {
			// get the container ID from the request
			containerID := c.Params("id")
			// get the container from the map
			container, ok := RunningContainers[containerID]
			if !ok {
				logger.Error().Msg("container not found")
				return
			}
			scheme := "ws"
			if container.IsEndpointTLS {
				scheme = "wss"
			}

			if err := WebsocketProxy(c, fmt.Sprintf("%s://%s/websockify", scheme, container.Endpoint)); err != nil {
				logger.Error().Err(err).Msg("websocket proxy error")
			}
		})(c)
	})

	if config.Webserver.EnableTLS {
		// start the server with TLS
		logger.Fatal().Err(app.ListenTLS(fmt.Sprintf(config.Webserver.Listen), config.Webserver.TLSCert, config.Webserver.TLSKey)).Msg("failed to start server")
	} else {

		logger.Fatal().Err(app.Listen(fmt.Sprintf(config.Webserver.Listen))).Msg("failed to start server")
	}
}
