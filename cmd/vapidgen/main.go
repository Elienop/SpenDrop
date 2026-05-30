// vapidgen prints a fresh VAPID keypair for Web Push. Run once per
// deployment and paste the values into VAPID_PUBLIC_KEY / VAPID_PRIVATE_KEY.
// The keys are base64url (raw, unpadded) strings as produced by
// webpush.GenerateVAPIDKeys; the same encoding the browser PushManager and
// the internal/push sender expect.
package main

import (
	"fmt"
	"os"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	// NOTE: GenerateVAPIDKeys returns (private, public, err) — private FIRST.
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate vapid keys: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("VAPID_PUBLIC_KEY=%s\n", pub)
	fmt.Printf("VAPID_PRIVATE_KEY=%s\n", priv)
}
