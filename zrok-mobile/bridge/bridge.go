package bridge

import (
	"fmt"

	"github.com/openziti/zrok/v2/sdk/golang/sdk"
)

// SDKCompileProbe forces the prototype to compile against the real zrok SDK.
// It does not load an identity, contact the zrok service, or create a share.
func SDKCompileProbe() string {
	_ = sdk.CreateShare
	_ = sdk.DeleteShare
	_ = sdk.NewListener
	_ = sdk.NewDialer

	request := &sdk.ShareRequest{
		BackendMode: sdk.TcpTunnelBackendMode,
		ShareMode:   sdk.PublicShareMode,
		Target:      "127.0.0.1:8080",
	}
	if request.Target == "" {
		return "zrok SDK request construction failed"
	}
	return fmt.Sprintf("zrok SDK v2 bridge compiled; backend=%s; mode=%s",
		request.BackendMode, request.ShareMode)
}
