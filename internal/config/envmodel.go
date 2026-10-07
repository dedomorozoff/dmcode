package config

import (
	"errors"
	"os"
	"strings"
)

// EnvModelKey is the .env variable that names the model to run.
const EnvModelKey = "DMCODE_MODEL"

// ErrProviderNotInEnv says the .env does not name the endpoint this session is
// talking to, so there is nowhere honest to record a model for it. It is a named
// error rather than a string so a caller can tell "not saved, and here is why"
// apart from "the write failed", and so a test can assert the distinction.
var ErrProviderNotInEnv = errors.New("this provider is not the one .env configures")

// SaveModel records the model in .env so a model picked in the running session
// is the one the next start uses, and reports whether it was saved.
//
// It refuses to write when the file does not already describe this endpoint.
// DMCODE_MODEL is honoured on the two configured paths — an explicit
// OPENAI_BASE_URL, or a provider key that names a preset — and ignored by the
// free-endpoint discovery path, which asks each candidate what it serves.
// Writing the line anyway would either do nothing today, or apply tomorrow to
// whatever endpoint the same .env describes by then: a model name is only
// meaningful next to the endpoint it belongs to, and a wrong one is a start-up
// failure with nothing on screen to say why.
func SaveModel(p Provider, model string) error {
	lines, err := ReadDotEnv()
	if err != nil {
		return err
	}
	if !EnvDescribesProvider(lines, p) {
		return ErrProviderNotInEnv
	}
	merged, _ := MergeDotEnv(lines, map[string]string{EnvModelKey: model})
	return WriteDotEnv(merged)
}

// EnvDescribesProvider reports whether the .env lines already say which endpoint
// p is, by either of the two ways the startup path is told: an explicit
// OPENAI_BASE_URL naming this host, or the key variable of the setup option that
// does. A line naming a different endpoint is not a description of this one, and
// neither is a comment that happens to mention it.
func EnvDescribesProvider(lines []string, p Provider) bool {
	if p.BaseURL == "" {
		return false
	}
	host := endpointHost(p.BaseURL)
	if host == "" {
		return false
	}
	for _, line := range lines {
		k, v, ok := DotEnvPair(line)
		if !ok {
			continue
		}
		if k == "OPENAI_BASE_URL" && endpointHost(v) == host {
			return true
		}
		if o, found := optionForKey(k); found && endpointHost(o.BaseURL) == host {
			return true
		}
	}
	return false
}

// optionForKey finds the setup option whose key variable is name.
func optionForKey(name string) (SetupOption, bool) {
	for _, o := range SetupOptions() {
		if o.EnvKey != "" && o.EnvKey == name {
			return o, true
		}
	}
	return SetupOption{}, false
}

// WriteDotEnv writes the .env file with the permissions it needs — it holds
// provider keys — and in the order that cannot lose it: a temporary file in the
// same directory, then a rename over the original. A plain write truncates the
// file first, so a crash or a full disk in between leaves a session with no
// configuration at all and no way back to the one it had.
func WriteDotEnv(lines []string) error {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l + "\n")
	}
	if err := os.WriteFile(".env.tmp", []byte(sb.String()), 0o600); err != nil {
		return err
	}
	if err := os.Rename(".env.tmp", ".env"); err != nil {
		os.Remove(".env.tmp")
		return err
	}
	return nil
}
