package spacenotify

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// refuseUndeclaredSecrets is spec 03 step 6: a route naming a secret other
// than the one v1 declares is refused BEFORE anything is emitted — by
// route (index + chat, since a route carries no id of its own) and by
// name, never discovered three steps later inside the sender as a missing
// environment variable. One bad route among any number of good ones
// refuses the WHOLE render — no partial output.
func refuseUndeclaredSecrets(routes []space.NotificationRoute) error {
	for i, r := range routes {
		secret := r.Secret
		if secret == "" {
			secret = DeclaredSecret
		}
		if secret != DeclaredSecret {
			return fmt.Errorf("notify render: route #%d (chat %s) names secret %q, but v1 declares only %q", i, r.Chat, secret, DeclaredSecret)
		}
	}
	return nil
}
