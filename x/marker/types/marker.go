package types

import (
	"errors"
	"fmt"
	"strings"

	sdkmath "cosmossdk.io/math"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	proto "github.com/cosmos/gogoproto/proto"
)

var (
	// ensure the MarkerAccount correctly extends the following interfaces
	_ sdk.AccountI             = (*MarkerAccount)(nil)
	_ authtypes.GenesisAccount = (*MarkerAccount)(nil)
	_ MarkerAccountI           = (*MarkerAccount)(nil)
)

// MarkerAccountI defines the required method interface for a marker account
type MarkerAccountI interface {
	proto.Message

	sdk.AccountI
	Clone() *MarkerAccount

	Validate() error

	GetDenom() string
	GetManager() sdk.AccAddress
	GetMarkerType() MarkerType

	GetStatus() MarkerStatus
	SetStatus(MarkerStatus) error

	GetSupply() sdk.Coin
	SetSupply(sdk.Coin) error
	HasFixedSupply() bool

	GrantAccess(AccessGrantI) error
	RevokeAccess(sdk.AccAddress) error
	GetAccessList() []AccessGrant

	HasAccess(string, Access) bool
	ValidateHasAccess(string, Access) error
	AddressHasAccess(sdk.AccAddress, Access) bool
	ValidateAddressHasAccess(sdk.AccAddress, Access) error
	AddressListForPermission(Access) []sdk.AccAddress

	HasGovernanceEnabled() bool

	AllowsForcedTransfer() bool
	SetAllowForcedTransfer(bool)

	GetRequiredAttributes() []string
	SetRequiredAttributes([]string)
}

// NewEmptyMarkerAccount creates a new empty marker account in a Proposed state
func NewEmptyMarkerAccount(denom, manager string, grants []AccessGrant) *MarkerAccount {
	baseAcc := authtypes.NewBaseAccountWithAddress(MustGetMarkerAddress(denom))
	return &MarkerAccount{
		BaseAccount:            baseAcc,
		AccessControl:          grants,
		Denom:                  denom,
		Manager:                manager,
		Supply:                 sdkmath.ZeroInt(),
		Status:                 StatusProposed,
		MarkerType:             MarkerType_Coin,
		SupplyFixed:            true,
		AllowGovernanceControl: true,
		AllowForcedTransfer:    false,
	}
}

// NewMarkerAccount creates a marker account initialized over a given base account.
func NewMarkerAccount(
	baseAcc *authtypes.BaseAccount,
	totalSupply sdk.Coin,
	manager sdk.AccAddress,
	accessControls []AccessGrant,
	status MarkerStatus,
	markerType MarkerType,
	supplyFixed, allowGovernanceControl, allowForcedTransfer bool,
	requiredAttributes []string,
) *MarkerAccount {
	// clear marker manager for active or later status accounts.
	if status >= StatusActive {
		manager = sdk.AccAddress{}
	}
	return &MarkerAccount{
		BaseAccount:            baseAcc,
		Denom:                  totalSupply.Denom,
		Manager:                manager.String(),
		Supply:                 totalSupply.Amount,
		AccessControl:          accessControls,
		Status:                 status,
		MarkerType:             markerType,
		SupplyFixed:            supplyFixed,
		AllowGovernanceControl: allowGovernanceControl,
		AllowForcedTransfer:    allowForcedTransfer,
		RequiredAttributes:     requiredAttributes,
	}
}

// Clone makes a MarkerAccount instance copy
func (ma MarkerAccount) Clone() *MarkerAccount {
	return proto.Clone(&ma).(*MarkerAccount)
}

// GetDenom the denomination of the coin associated with this marker
func (ma MarkerAccount) GetDenom() string { return ma.Denom }

// HasFixedSupply return true if the total supply for the marker is considered "fixed" and should be controlled with an
// invariant check
func (ma MarkerAccount) HasFixedSupply() bool { return ma.SupplyFixed }

// HasGovernanceEnabled returns true if this marker allows governance proposals to control this marker
func (ma MarkerAccount) HasGovernanceEnabled() bool { return ma.AllowGovernanceControl }

// AllowsForcedTransfer returns true if force transfer is allowed for this marker.
func (ma MarkerAccount) AllowsForcedTransfer() bool {
	return ma.AllowForcedTransfer
}

func (ma *MarkerAccount) SetAllowForcedTransfer(allowForcedTransfer bool) {
	ma.AllowForcedTransfer = allowForcedTransfer
}

// HasAccess returns true if the provided address has been assigned the provided
// role within the current MarkerAccount AccessControl
func (ma *MarkerAccount) HasAccess(addr string, role Access) bool {
	for _, g := range ma.AccessControl {
		if g.Address == addr && g.HasAccess(role) {
			return true
		}
	}
	return false
}

// ValidateHasAccess returns an error if the provided address does not have the given role in this marker.
func (ma *MarkerAccount) ValidateHasAccess(addr string, role Access) error {
	if !ma.HasAccess(addr, role) {
		return fmt.Errorf("%s does not have %s on %s marker (%s)", addr, role, ma.GetDenom(), ma.GetAddress())
	}
	return nil
}

// AddressHasAccess returns true if the provided address has been assigned the provided
// role within the current MarkerAccount AccessControl
func (ma *MarkerAccount) AddressHasAccess(addr sdk.AccAddress, role Access) bool {
	return ma.HasAccess(addr.String(), role)
}

// ValidateAddressHasAccess returns an error if the provided address does not have the given role in this marker.
func (ma *MarkerAccount) ValidateAddressHasAccess(addr sdk.AccAddress, role Access) error {
	return ma.ValidateHasAccess(addr.String(), role)
}

// AtLeastOneAddrHasAccess returns true if one or more of the provided addrs has the given role on this marker.
func AtLeastOneAddrHasAccess(ma MarkerAccountI, addrs []sdk.AccAddress, role Access) bool {
	for _, addr := range addrs {
		if ma.HasAccess(addr.String(), role) {
			return true
		}
	}
	return false
}

// ValidateAtLeastOneAddrHasAccess returns an error if there isn't an entry in addrs that has the given role in this marker.
func ValidateAtLeastOneAddrHasAccess(ma MarkerAccountI, addrs []sdk.AccAddress, role Access) error {
	if len(addrs) == 1 {
		return ma.ValidateHasAccess(addrs[0].String(), role)
	}
	if !AtLeastOneAddrHasAccess(ma, addrs, role) {
		strs := make([]string, len(addrs))
		for i, addr := range addrs {
			strs[i] = addr.String()
		}
		return fmt.Errorf("none of %q have permission %s on %s marker (%s)", strs, role, ma.GetDenom(), ma.GetAddress())
	}
	return nil
}

// AddressListForPermission returns a list of all addresses with the provided rule within the
// current MarkerAccount AccessControl list
func (ma *MarkerAccount) AddressListForPermission(role Access) []sdk.AccAddress {
	var addressList []sdk.AccAddress

	for _, g := range ma.AccessControl {
		if g.HasAccess(role) {
			addressList = append(addressList, g.GetAddress())
		}
	}
	return addressList
}

// Validate performs minimal sanity checking over the current MarkerAccount instance
func (ma MarkerAccount) Validate() error {
	if !ValidMarkerStatus(ma.Status) {
		return fmt.Errorf("invalid marker status")
	}
	// unlikely as this is set using Coin which prohibits negative values.
	if ma.Supply.IsNegative() {
		return fmt.Errorf("total supply must be greater than or equal to zero")
	}
	if ma.Status < StatusActive && ma.Manager == "" && len(ma.AddressListForPermission(Access_Admin)) == 0 {
		return fmt.Errorf("a manager is required if there are no accounts with ACCESS_ADMIN and marker is not ACTIVE")
	}
	if ma.Status == StatusFinalized && len(ma.AddressListForPermission(Access_Mint)) == 0 && ma.Supply.IsZero() {
		return fmt.Errorf("cannot create a marker with zero total supply and no authorization for minting more")
	}
	// unlikely as this is set using a Coin which prohibits this value.
	if strings.TrimSpace(ma.Denom) == "" {
		return fmt.Errorf("marker denom cannot be empty")
	}
	markerAddress, err := MarkerAddress(ma.Denom)
	if err != nil {
		return fmt.Errorf("marker denom is invalid: %w", err)
	}
	if err := ValidateIbcDenom(ma); err != nil {
		return fmt.Errorf("invalid ibc denom configuration: %w", err)
	}
	if !ma.BaseAccount.GetAddress().Equals(markerAddress) {
		return fmt.Errorf("address %s cannot be derived from the marker denom '%s'", ma.Address, ma.Denom)
	}
	if err := ValidateGrantsForMarkerType(ma.MarkerType, ma.AccessControl...); err != nil {
		return fmt.Errorf("invalid access privileges granted: %w", err)
	}
	selfGrant := GrantsForAddress(ma.GetAddress(), ma.AccessControl...).GetAccessList()
	if len(selfGrant) > 0 {
		return fmt.Errorf("permissions cannot be granted to '%s' marker account: %v", ma.Denom, selfGrant)
	}
	if ma.Manager == ma.GetAddress().String() {
		return fmt.Errorf("marker can not be self managed")
	}
	if ma.AllowForcedTransfer && ma.MarkerType != MarkerType_RestrictedCoin {
		return fmt.Errorf("forced transfers can only be allowed on restricted markers")
	}
	return ma.BaseAccount.Validate()
}

// ValidateIbcDenom if denom is ibc it that validates supply is not fixed and Mint/Burn is not allowed
func ValidateIbcDenom(ma MarkerAccount) error {
	if !strings.HasPrefix(ma.Denom, "ibc/") {
		return nil
	}
	if ma.SupplyFixed {
		return errors.New("fixed supply is not supported for ibc marker")
	}
	for _, grant := range ma.AccessControl {
		for _, access := range grant.Permissions {
			if access.IsOneOf(Access_Burn, Access_Mint) {
				return fmt.Errorf("%v is not supported for ibc marker", access)
			}
		}
	}
	return nil
}

// ValidateGrantsForMarkerType checks a collection of grants and returns any errors encountered or nil
func ValidateGrantsForMarkerType(markerType MarkerType, grants ...AccessGrant) error {
	for _, grant := range grants {
		for _, access := range grant.Permissions {
			switch markerType {
			case MarkerType_Coin:
				{
					if !access.IsOneOf(Access_Admin, Access_Burn, Access_Delete, Access_Deposit, Access_Mint, Access_Withdraw) {
						return fmt.Errorf("%v is not supported for marker type %v", access, markerType)
					}
				}
			// Restricted Coins also support Transfer access
			case MarkerType_RestrictedCoin:
				{
					if !access.IsOneOf(Access_Admin, Access_Burn, Access_Delete, Access_Deposit, Access_Mint, Access_Withdraw, Access_Transfer, Access_ForceTransfer) {
						return fmt.Errorf("%v is not supported for marker type %v", access, markerType)
					}
				}
			default:
				return fmt.Errorf("cannot validate access grants for unsupported marker type %s", markerType.String())
			}
		}
	}
	return ValidateGrants(grants...)
}

// ValidateRequiredAttributes checks that required attributes are of the correct format
func ValidateRequiredAttributes(requiredAttributes []string) error {
	for _, attr := range requiredAttributes {
		if strings.TrimSpace(attr) == "" {
			return fmt.Errorf("invalid name: empty")
		}
	}
	return nil
}

func (ma *MarkerAccount) GetRequiredAttributes() []string {
	return ma.RequiredAttributes
}

func (ma *MarkerAccount) SetRequiredAttributes(requiredAttributes []string) {
	ma.RequiredAttributes = requiredAttributes
}

// GetPubKey implements authtypes.Account (but there are no public keys associated with the account for signing)
func (ma MarkerAccount) GetPubKey() cryptotypes.PubKey {
	return nil
}

// SetPubKey implements authtypes.Account (but there are no public keys associated with the account for signing)
func (ma *MarkerAccount) SetPubKey(_ cryptotypes.PubKey) error {
	return fmt.Errorf("not supported for marker accounts")
}

// SetSequence implements authtypes.Account (but you can't set a sequence as you can't sign tx for this account)
func (ma *MarkerAccount) SetSequence(_ uint64) error {
	return fmt.Errorf("not supported for marker accounts")
}

// GetStatus returns the status of the marker account.
func (ma MarkerAccount) GetStatus() MarkerStatus {
	return ma.Status
}

// SetStatus sets the status of the marker to the provided value.
func (ma *MarkerAccount) SetStatus(status MarkerStatus) error {
	if status == StatusUndefined {
		return fmt.Errorf("error invalid marker status %s", status)
	}
	if status == StatusActive {
		// When activated the manager property is no longer valid so clear it
		ma.Manager = ""
	}

	ma.Status = status
	return nil
}

// GetMarkerType returns the type of the marker account.
func (ma MarkerAccount) GetMarkerType() MarkerType {
	return ma.MarkerType
}

// GetAddress returns the address of the marker account.
func (ma MarkerAccount) GetAddress() sdk.AccAddress {
	addr, err := sdk.AccAddressFromBech32(ma.Address)
	if err != nil {
		panic(err)
	}
	return addr
}

// GetManager returns the address of the account that is responsible for the proposed marker.
func (ma MarkerAccount) GetManager() sdk.AccAddress {
	// manage is not required, return an empty address if not set.
	if ma.Manager == "" {
		return sdk.AccAddress{}
	}
	// if the manager is set we much return it as an address
	addr, err := sdk.AccAddressFromBech32(ma.Manager)
	if err != nil {
		panic(err)
	}
	return addr
}

// SetManager sets the manager/owner address for proposed marker accounts
func (ma *MarkerAccount) SetManager(manager sdk.AccAddress) error {
	if !manager.Empty() && ma.Status != StatusProposed {
		return fmt.Errorf("manager address is only valid for proposed markers, use access grants instead")
	}
	if err := sdk.VerifyAddressFormat(manager); err != nil {
		return err
	}
	ma.Manager = manager.String()
	return nil
}

// SetSupply sets the total supply amount to track
func (ma *MarkerAccount) SetSupply(total sdk.Coin) error {
	if total.Denom != ma.Denom {
		return fmt.Errorf("supply coin denom must match marker denom")
	}
	ma.Supply = total.Amount
	return nil
}

// GetSupply implements authtypes.Account
func (ma MarkerAccount) GetSupply() sdk.Coin {
	return sdk.NewCoin(ma.Denom, ma.Supply)
}

// GrantAccess appends the access grant to the marker account.
func (ma *MarkerAccount) GrantAccess(access AccessGrantI) error {
	if err := access.Validate(); err != nil {
		return err
	}
	// Find any existing permissions and append specified permissions
	for _, ac := range ma.AccessControl {
		if ac.GetAddress().Equals(access.GetAddress()) {
			if err := access.MergeAdd(*NewAccessGrant(ac.GetAddress(), ac.GetAccessList())); err != nil {
				return err
			}
		}
	}
	// Revoke existing (no errors from this as we have validated above)
	if err := ma.RevokeAccess(access.GetAddress()); err != nil {
		return err
	}
	// Append the new record
	ma.AccessControl = append(ma.AccessControl, *NewAccessGrant(access.GetAddress(), access.GetAccessList()))
	return nil
}

// RevokeAccess removes any AccessGrant for the given address on this marker.
func (ma *MarkerAccount) RevokeAccess(addr sdk.AccAddress) error {
	if err := sdk.VerifyAddressFormat(addr); err != nil {
		return fmt.Errorf("can not revoke access for invalid address")
	}

	var accessList []AccessGrant
	for _, ac := range ma.AccessControl {
		if ac.Address != addr.String() {
			accessList = append(accessList, ac)
		}
	}

	ma.AccessControl = accessList
	return nil
}

// GetAccessList returns the full access list for the marker
func (ma *MarkerAccount) GetAccessList() []AccessGrant {
	return ma.AccessControl
}

// MarkerTypeFromString returns a MarkerType from a string. It returns an error
// if the string is invalid.
func MarkerTypeFromString(str string) (MarkerType, error) {
	switch strings.ToLower(str) {
	case "coin":
		return MarkerType_Coin, nil
	case "restricted":
		fallthrough
	case "restrictedcoin":
		return MarkerType_RestrictedCoin, nil

	default:
		if val, ok := MarkerType_value[str]; ok {
			return MarkerType(val), nil
		}
	}

	return MarkerType_Unknown, fmt.Errorf("'%s' is not a valid marker status", str)
}

// AddToRequiredAttributes add new attributes to current list, errors if attribute already exists
func AddToRequiredAttributes(currentAttrs []string, addAttrs []string) ([]string, error) {
	for _, aa := range addAttrs {
		for _, ca := range currentAttrs {
			if aa == ca {
				return nil, fmt.Errorf("attribute %q is already required", aa)
			}
		}
		currentAttrs = append(currentAttrs, aa)
	}
	return currentAttrs, nil
}

// RemoveFromRequiredAttributes remove  attributes from current list, errors if attribute does not exists
func RemoveFromRequiredAttributes(currentAttrs []string, removeAttrs []string) ([]string, error) {
	toRem := make(map[string]bool)
	for _, ra := range removeAttrs {
		found := false
		for _, ca := range currentAttrs {
			if ra == ca {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("attribute %q is already not required", ra)
		}
		toRem[ra] = true
	}
	reqAttrs := make([]string, 0, len(currentAttrs))
	for _, ca := range currentAttrs {
		if !toRem[ca] {
			reqAttrs = append(reqAttrs, ca)
		}
	}
	return reqAttrs, nil
}

// NewNetAssetValue returns a new instance of NetAssetValue
func NewNetAssetValue(price sdk.Coin, volume uint64) NetAssetValue {
	return NetAssetValue{
		Price:  price,
		Volume: volume,
	}
}

// Validate returns error if NetAssetValue is not in a valid state
func (mnav *NetAssetValue) Validate() error {
	if err := mnav.Price.Validate(); err != nil {
		return err
	}

	if mnav.Price.Amount.GT(sdkmath.NewInt(0)) && mnav.Volume < 1 {
		return fmt.Errorf("marker net asset value volume must be positive value")
	}

	return nil
}
