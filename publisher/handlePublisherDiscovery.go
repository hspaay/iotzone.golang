package publisher

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/iotdomain/iotdomain-go/messaging"
	"github.com/iotdomain/iotdomain-go/publishers"
	"github.com/iotdomain/iotdomain-go/types"
	"github.com/sirupsen/logrus"
	"github.com/square/go-jose"
)

// handleDSSDiscovery discoveres the identity of the domain security service
// The DSS publish signing key is used to verify the identity of all publishers
// Without a DSS, all publishers are unverified.
func (publisher *Publisher) handleDSSDiscovery(dssIdentityMsg *types.PublisherIdentityMessage) {
	// Verify the identity of the DSS
	// TODO: CA support. For now assume address protection is used so this is trusted.

	// dssSigningPem := dssIdentity.Identity.PublicKeySigning
	// dssSigningKey := messaging.PublicKeyFromPem(dssSigningPem)
	// publisher.dssSigningKey = dssSigningKey
	publisher.domainPublishers.UpdatePublisher(dssIdentityMsg)
	logrus.Infof("handleDSSDiscovery: %s", dssIdentityMsg.Address)
}

// handlePublisherDiscovery collects and saves remote publishers
// Intended for discovery of available publishers and for verification of signatures of
// configuration and input messages received from these publishers.
// Handle the following trust scenarios:
//  A: Discovery of the DSS. Address protection or use a CA.
//  B: Trust address protection - always accept the publisher if its message is signed by itself
//  C: Trust DSS signing - verify identity is signed by DSS
//
// address contains the publisher's identity address: <domain>/<publisher>/$identity
// message contains the publisher identity message
func (publisher *Publisher) handlePublisherDiscovery(address string, message string) error {
	var pubIdentityMsg *types.PublisherIdentityMessage
	var payload string

	// message can be signed or not signed so start with trying to parse
	jseSignature, err := jose.ParseSigned(string(message))
	if err != nil {
		// message isn't signed
		if publisher.signMessages {
			// message must be signed though. Discard
			errText := fmt.Sprintf("handlePublisherDiscovery: Publisher update isn't signed but only signed updates are accepted. Publisher: %s", address)
			logrus.Warn(errText)
			return errors.New(errText)
		}
		// accept the unsigned message as signing isn't required
		payload = message
	} else {
		// message is signed. The signature must verify with the publisher signing key included in the message
		payload = string(jseSignature.UnsafePayloadWithoutVerification())
	}

	err = json.Unmarshal([]byte(payload), &pubIdentityMsg)
	if err != nil {
		// abort
		errText := fmt.Sprintf("handlePublisherDiscovery: Failed parsing json payload [unsigned]: %s", err)
		logrus.Warn(errText)
		return errors.New(errText)
	}

	// Handle the DSS publisher separately
	dssAddress := publishers.MakePublisherIdentityAddress(publisher.Domain(), types.DSSPublisherID)
	if address == dssAddress {
		publisher.handleDSSDiscovery(pubIdentityMsg)
	}

	// So we have a publisher identity update. Determine if it is trusted.
	// 1: No DSS, assume address protection is in place
	// 2: Do we have a DSS? If so, require the identity is signed by the DSS
	dssSigningKey := publisher.domainPublishers.GetPublisherKey(dssAddress)
	if dssSigningKey == nil {
		// 1: No DSS, assume address protection is in place
		publisher.domainPublishers.UpdatePublisher(pubIdentityMsg)
		logrus.Infof("handlePublisherDiscovery: Discovered publisher %s. [No DSS present]", address)

	} else {
		// 2: We have a DSS. Require the publisher identity is signed by the DSS
		// Create base64 encoded identity
		identityAsJSON, err := json.Marshal(pubIdentityMsg)
		if err != nil {
			errText := fmt.Sprintf("handlePublisherDiscovery: Missing identity for %s", address)
			logrus.Warn(errText)
			return errors.New(errText)
		}
		base64URLIdentity := base64.URLEncoding.EncodeToString(identityAsJSON)
		valid := messaging.VerifyEcdsaSignature(base64URLIdentity, pubIdentityMsg.IdentitySignature, dssSigningKey)
		if !valid {
			errText := fmt.Sprintf("handlePublisherDiscovery: Identity for %s doesn't have a valid DSS signature", address)
			logrus.Warn(errText)
			return errors.New(errText)
		}
		// finally, The newly published identity is correctly signed by the DSS
		publisher.domainPublishers.UpdatePublisher(pubIdentityMsg)
		logrus.Infof("Discovered publisher %s. [DSS verified]", address)
	}
	return err
}
