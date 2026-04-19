// Package sshutil provides a reusable SSH client for key-based authentication.
// All remote operations (HAProxy reload, CPU measurement, stress-ng) go through here.
package sshutil

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Client holds connection parameters for a remote host.
type Client struct {
	Host      string // IP or hostname (e.g. "127.0.0.1")
	Port      int    // SSH port (e.g. 22 or 2222 for NAT forwarding)
	User      string // SSH user (e.g. "debian")
	KeyPath   string // absolute path to private key file
	sshConfig *ssh.ClientConfig
}

// New creates a Client and parses the private key immediately so errors
// surface at startup rather than at first use.
func New(host string, port int, user, keyPath string) (*Client, error) {
	c := &Client{Host: host, Port: port, User: user, KeyPath: keyPath}
	if err := c.buildConfig(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) buildConfig() error {
	key, err := os.ReadFile(c.KeyPath)
	if err != nil {
		return fmt.Errorf("sshutil: read key %q: %w", c.KeyPath, err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return fmt.Errorf("sshutil: parse key: %w", err)
	}
	c.sshConfig = &ssh.ClientConfig{
		User:            c.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // acceptable in lab; use known_hosts in prod
		Timeout:         5 * time.Second,
	}
	return nil
}

// Run executes a command on the remote host and returns combined stdout+stderr.
func (c *Client) Run(command string) (string, error) {
	addr := fmt.Sprintf("%s:%d", c.Host, c.Port)
	conn, err := ssh.Dial("tcp", addr, c.sshConfig)
	if err != nil {
		return "", fmt.Errorf("sshutil: dial %s: %w", addr, err)
	}
	defer conn.Close()

	sess, err := conn.NewSession()
	if err != nil {
		return "", fmt.Errorf("sshutil: new session: %w", err)
	}
	defer sess.Close()

	out, err := sess.CombinedOutput(command)
	return strings.TrimSpace(string(out)), err
}

// WriteFile writes content to a remote file using a heredoc via SSH.
func (c *Client) WriteFile(remotePath, content string) error {
	// Use tee to write the file; escape single quotes in content
	escaped := strings.ReplaceAll(content, "'", `'"'"'`)
	cmd := fmt.Sprintf("echo '%s' | sudo tee %s > /dev/null", escaped, remotePath)
	_, err := c.Run(cmd)
	return err
}

// Reload sends a SIGHUP-equivalent to a systemd service.
func (c *Client) ReloadService(service string) error {
	_, err := c.Run("sudo systemctl reload " + service)
	return err
}
