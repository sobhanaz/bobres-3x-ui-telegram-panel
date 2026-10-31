package sui

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
)

// seq is the alphabet of the panel's random strings (RandomUtil.randomSeq).
const seq = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func randomSeq(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(seq)))
	for i := range b {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = seq[k.Int64()]
	}
	return string(b), nil
}

func randomB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func randomUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// RandomConfigs makes a new client's credentials for every protocol the panel
// knows, as the panel's own "add client" form does (s-ui-frontend,
// src/types/clients.ts randomConfigs): the panel takes a client's configuration
// as given and builds its users and share links from it.
func RandomConfigs(name string) (map[string]map[string]any, error) {
	pw, err := randomSeq(10)
	if err != nil {
		return nil, err
	}
	ss16, err := randomB64(16)
	if err != nil {
		return nil, err
	}
	ss32, err := randomB64(32)
	if err != nil {
		return nil, err
	}
	id, err := randomUUID()
	if err != nil {
		return nil, err
	}
	snell, err := randomSeq(32)
	if err != nil {
		return nil, err
	}
	return map[string]map[string]any{
		"mixed":         {"username": name, "password": pw},
		"socks":         {"username": name, "password": pw},
		"http":          {"username": name, "password": pw},
		"shadowsocks":   {"name": name, "password": ss32},
		"shadowsocks16": {"name": name, "password": ss16},
		"shadowtls":     {"name": name, "password": ss32},
		"vmess":         {"name": name, "uuid": id, "alterId": 0},
		"vless":         {"name": name, "uuid": id, "flow": "xtls-rprx-vision"},
		"anytls":        {"name": name, "password": pw},
		"trojan":        {"name": name, "password": pw},
		"naive":         {"username": name, "password": pw},
		"hysteria":      {"name": name, "auth_str": pw},
		"snell":         {"name": name, "userkey": snell},
		"tuic":          {"name": name, "uuid": id, "password": pw},
		"hysteria2":     {"name": name, "password": pw},
	}, nil
}
