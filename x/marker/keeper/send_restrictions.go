package keeper

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	internalsdk "github.com/provenance-io/provenance/internal/sdk"
	attrTypes "github.com/provenance-io/provenance/x/attribute/types"
	"github.com/provenance-io/provenance/x/marker/types"
)

var _ banktypes.SendRestrictionFn = Keeper{}.SendRestrictionFn

func (k Keeper) SendRestrictionFn(goCtx context.Context, fromAddr, toAddr sdk.AccAddress, amt sdk.Coins) (sdk.AccAddress, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	// In some cases, it might not be possible to add a bypass to the context.
	// If it's from either the Marker or IBC Transfer module accounts, assume proper validation has been done elsewhere.
	if types.HasBypass(ctx) || fromAddr.Equals(k.markerModuleAddr) || fromAddr.Equals(k.ibcTransferModuleAddr) {
		// But still don't let restricted denoms get sent to the fee collector.
		if toAddr.Equals(k.feeCollectorAddr) {
			for _, coin := range amt {
				markerAddr := types.MustGetMarkerAddress(coin.Denom)
				marker, err := k.GetMarker(ctx, markerAddr)
				if err != nil {
					return nil, err
				}
				if marker != nil && marker.GetMarkerType() == types.MarkerType_RestrictedCoin {
					return nil, fmt.Errorf("cannot send restricted denom %s to the fee collector", coin.Denom)
				}
			}
		}
		return toAddr, nil
	}

	// If it's coming from a marker, make sure the withdraw is allowed.
	admins := types.GetTransferAgents(ctx)
	if fromMarker, _ := k.GetMarker(ctx, fromAddr); fromMarker != nil {
		// The only ways to legitimately send from a marker account is to have a transfer agent with
		// withdraw permissions, or through a feegrant. The only way to have a feegrant from
		// a marker account is if an admin creates one using the marker module's GrantAllowance endpoint.
		// So if a feegrant is in use, that means the sending of these funds is authorized.
		// We also assume that other stuff is handling the actual checking and use of that feegrant,
		// so we don't need to worry about its details in here, and that HasFeeGrantInUse is only ever
		// true when collecting fees.
		if !internalsdk.HasFeeGrantInUse(ctx) {
			if len(admins) == 0 {
				return nil, fmt.Errorf("cannot withdraw from marker account %s (%s)",
					fromAddr.String(), fromMarker.GetDenom())
			}

			// Need at least one admin that can make withdrawals.
			if err := types.ValidateAtLeastOneAddrHasAccess(fromMarker, admins, types.Access_Withdraw); err != nil {
				return nil, err
			}
		}

		// Check to see if marker is active; the coins created by a marker can only be withdrawn when it is active.
		// Any other coins that may be present (collateralized assets?) can be transferred.
		if fromMarker.GetStatus() != types.StatusActive {
			hasFromCoin, fromAmt := amt.Find(fromMarker.GetDenom())
			if hasFromCoin && !fromAmt.IsZero() {
				return nil, fmt.Errorf("cannot withdraw %s from %s marker (%s): marker status (%s) is not %s",
					fromAmt, fromMarker.GetDenom(), fromAddr, fromMarker.GetStatus(), types.StatusActive)
			}
		}
	}

	// If it's going to a restricted marker, either an admin (if there is one) or
	// fromAddr (if there isn't an admin) must have deposit access on that marker.
	toMarker, _ := k.GetMarker(ctx, toAddr)
	if toMarker != nil && toMarker.GetMarkerType() == types.MarkerType_RestrictedCoin {
		if len(admins) > 0 {
			if err := types.ValidateAtLeastOneAddrHasAccess(toMarker, admins, types.Access_Deposit); err != nil {
				return nil, err
			}
		} else {
			if err := toMarker.ValidateAddressHasAccess(fromAddr, types.Access_Deposit); err != nil {
				return nil, err
			}
		}
	}

	// Check the ability to send each denom involved.
	for _, coin := range amt {
		if err := k.validateSendDenom(ctx, fromAddr, toAddr, admins, coin.Denom, toMarker); err != nil {
			return nil, err
		}
	}

	return toAddr, nil
}

// validateSendDenom makes sure a send of the given denom is allowed for the given addresses.
// This is NOT the validation that is needed for the marker Transfer endpoint.
func (k Keeper) validateSendDenom(ctx sdk.Context, fromAddr, toAddr sdk.AccAddress, admins []sdk.AccAddress, denom string, toMarker types.MarkerAccountI) error {
	markerAddr := types.MustGetMarkerAddress(denom)
	marker, err := k.GetMarker(ctx, markerAddr)
	if err != nil {
		return err
	}

	// If there's a marker, it must be active.
	if marker != nil && marker.GetStatus() != types.StatusActive {
		return fmt.Errorf("cannot send %s coins: marker status (%s) is not %s", denom, marker.GetStatus(), types.StatusActive)
	}

	// If there's no marker for the denom, or it's not a restricted marker, there's nothing more to do here.
	if marker == nil || marker.GetMarkerType() != types.MarkerType_RestrictedCoin {
		return nil
	}

	// We can't allow restricted coins to end up with the fee collector.
	if toAddr.Equals(k.feeCollectorAddr) {
		return fmt.Errorf("restricted denom %s cannot be sent to the fee collector", denom)
	}

	// If there's an admin that has transfer access, it's not a normal bank send and there's nothing more to do here.
	if len(admins) > 0 && types.AtLeastOneAddrHasAccess(marker, admins, types.Access_Transfer) {
		return nil
	}

	// If from address is in the deny list, prevent sending of restricted marker.
	// If the fromAddr is both on the send-deny list and has transfer access, we want to deny this send.
	// They can either take themselves off the list and do the send again, or just use the transfer endpoint.
	// But for normal sends (without a transfer agent), we want the send-deny list enforced first.
	if k.IsSendDeny(ctx, markerAddr, fromAddr) {
		return fmt.Errorf("%s is on deny list for sending restricted marker", fromAddr.String())
	}

	// If the fromAddr has transfer access, there's nothing left to check.
	if marker.AddressHasAccess(fromAddr, types.Access_Transfer) {
		return nil
	}

	// If going to a marker, transfer permission is required regardless of whether it's coming from a bypass.
	// If someone wants to deposit funds from a bypass account, they can either send the funds to a valid
	// intermediary account and deposit them from there, or give the bypass account deposit and transfer permissions.
	// It's assumed that a marker address cannot be in the bypass list.
	if toMarker != nil {
		if len(admins) == 0 {
			return fmt.Errorf("%s does not have %s on %s marker (%s)",
				fromAddr, types.Access_Transfer, denom, marker.GetAddress())
		}
		addrs := make([]string, 1+len(admins))
		addrs[0] = fromAddr.String()
		for i, admin := range admins {
			addrs[i+1] = admin.String()
		}
		return fmt.Errorf("none of %q have %s on %s marker (%s)",
			addrs, types.Access_Transfer, denom, marker.GetAddress())
	}

	// If there aren't any required attributes, transfer permission is required unless coming from a bypass account.
	// It's assumed that the only way the restricted coins without required attributes can get into a bypass
	// account is by someone with transfer permission, which is then conveyed for this transfer too.
	reqAttr := marker.GetRequiredAttributes()
	if len(reqAttr) == 0 {
		if k.IsReqAttrBypassAddr(fromAddr) {
			return nil
		}
		return fmt.Errorf("%s does not have transfer permissions for %s", fromAddr.String(), denom)
	}

	// At this point, we know there are required attributes and that fromAddr does not have transfer permission.
	// If the toAddress has a bypass, skip checking the attributes and allow the transfer.
	// When these funds are then being moved out of the bypass account, attributes are checked on that destination.
	if k.IsReqAttrBypassAddr(toAddr) {
		return nil
	}

	attributes, err := k.attrKeeper.GetAllAttributesAddr(ctx, toAddr)
	if err != nil {
		return fmt.Errorf("could not get attributes for %s: %w", toAddr.String(), err)
	}
	missing := findMissingAttributes(reqAttr, attributes)
	if len(missing) != 0 {
		pl := ""
		if len(missing) != 1 {
			pl = "s"
		}
		return fmt.Errorf("address %s does not contain the %q required attribute%s: \"%s\"", toAddr.String(), denom, pl, strings.Join(missing, `", "`))
	}

	return nil
}

// findMissingAttributes returns all entries in required that don't pass
// MatchAttribute on at least one of the provided attribute names.
func findMissingAttributes(required []string, attributes []attrTypes.Attribute) []string {
	var rv []string
reqLoop:
	for _, req := range required {
		for _, attr := range attributes {
			if MatchAttribute(req, attr.Name) {
				continue reqLoop
			}
		}
		rv = append(rv, req)
	}
	return rv
}

// NormalizeRequiredAttributes normalizes the required attribute names using name module's Normalize method
func (k Keeper) NormalizeRequiredAttributes(ctx sdk.Context, requiredAttributes []string) ([]string, error) {
	maxLength := int(k.attrKeeper.GetMaxValueLength(ctx))
	result := make([]string, len(requiredAttributes))
	for i, attr := range requiredAttributes {
		if len(attr) > maxLength {
			return nil, fmt.Errorf("required attribute %v length is too long %v : %v ", attr, len(attr), maxLength)
		}

		// for now just check if required attribute starts with a *.
		var prefix string
		if strings.HasPrefix(attr, "*.") {
			prefix = attr[:2]
			attr = attr[2:]
		}
		normalizedAttr, err := k.nameKeeper.Normalize(ctx, attr)
		if err != nil {
			return nil, err
		}
		result[i] = fmt.Sprintf("%s%s", prefix, normalizedAttr)
	}
	return result, nil
}

// MatchAttribute returns true if the provided attr satisfies the reqAttr.
func MatchAttribute(reqAttr string, attr string) bool {
	if len(reqAttr) < 1 {
		return false
	}
	if strings.HasPrefix(reqAttr, "*.") {
		// [1:] because we only want to ignore the '*'; the '.' needs to be part of the check.
		return strings.HasSuffix(attr, reqAttr[1:])
	}
	return reqAttr == attr
}
