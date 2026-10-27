package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	sdk "github.com/Chow-Code/dbx-bson-codec/internal/dbxpluginsdk"
)

const pluginID = "io.github.chow-code.redis-bson-codec"
const pluginVersion = "0.1.0"

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--bson-decode" || os.Args[1] == "--bson-encode") {
		if err := runBSONFilter(os.Args[1], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	dataDir := os.Getenv("DBX_PLUGIN_DATA_DIR")
	if dataDir == "" {
		fmt.Fprintln(os.Stderr, "DBX_PLUGIN_DATA_DIR is required")
		os.Exit(1)
	}
	handler := &plugin{configPath: filepath.Join(dataDir, "config.json"), slots: make(chan struct{}, 4)}
	server := sdk.NewServer(sdk.Metadata{ID: pluginID, Version: pluginVersion, Capabilities: []string{"redis-value-codec-v1"}}, handler)
	if err := server.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (p *plugin) Handle(_ sdk.RequestContext, method string, params json.RawMessage, _ *sdk.Emitter) (any, *sdk.PluginError) {
	var result any
	var err error
	switch method {
	case "config/get":
		result, err = p.loadConfig()
	case "config/save":
		var config Config
		err = parseStrict(params, &config)
		if err == nil {
			err = p.saveConfig(config)
		}
		result = map[string]bool{"success": err == nil}
	case "redis/codec/decode", "redis/codec/encode":
		select {
		case p.slots <- struct{}{}:
			defer func() { <-p.slots }()
		default:
			return nil, sdk.NewError(-32000, "Too many concurrent codec requests")
		}
		var request CodecRequest
		err = parseStrict(params, &request)
		if err == nil {
			result, err = p.transform(method, request)
		}
	default:
		return nil, sdk.MethodNotFound(method)
	}
	if err != nil {
		return nil, sdk.NewError(-32000, err.Error())
	}
	return result, nil
}
