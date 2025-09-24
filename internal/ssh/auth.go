package ssh

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// LoadAuthorizedKeys reads newline-separated authorized_keys format and returns a map of the
// canonical marshaled key (string) -> ssh.PublicKey for fast lookups.
func LoadAuthorizedKeys(keysData string) (map[string]ssh.PublicKey, error) {
	out := make(map[string]ssh.PublicKey)
	scanner := bufio.NewScanner(strings.NewReader(keysData))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("parse authorized key failed: %w", err)
		}
		out[string(ssh.MarshalAuthorizedKey(pub))] = pub
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no authorized keys loaded")
	}
	return out, nil
}
