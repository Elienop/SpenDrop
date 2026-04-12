package api

import (
	"context"
	"fmt"
)

// Domain string constants used across handlers.
//
// These values are wire-format identifiers stored in the database and
// exchanged with the frontend — changing them is a breaking schema change,
// not a refactor. Extracting them to named constants eliminates typo drift
// at call sites (a typo in "admin" would silently fail the auth check) and
// gives the linter a single place to audit role/type/setting usage.

// User role values stored in users.role.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// Category type values stored in categories.type. Used both for SQL
// comparisons and for validating category create/update requests.
const (
	CategoryTypeExpense = "expense"
	CategoryTypeIncome  = "income"
)

// App setting keys (app_settings.key). Prefer these constants over raw
// string literals so a typo at the call site becomes a compile error
// instead of a silent "setting not found" at runtime.
const (
	SettingRegistrationEnabled = "registration_enabled"
	SettingDefaultBudget       = "default_budget"
	SettingBaseCurrency        = "base_currency"
)

// DefaultBaseCurrency is the fallback used when the base_currency setting
// is missing or empty. Matches the seed value written by migration 001.
const DefaultBaseCurrency = "USD"

// dismissedRecurringKey returns the per-year app_settings key used to
// store the user's dismissed recurring-expense list. Keeping the format
// string in one place prevents the reader and writer from drifting out
// of sync (e.g. "dismissed_recurring_%d" vs. "dismissed-recurring-%d").
func dismissedRecurringKey(year int) string {
	return fmt.Sprintf("dismissed_recurring_%d", year)
}

// getBaseCurrency returns the configured base currency code from
// app_settings, falling back to DefaultBaseCurrency if the setting is
// missing, empty, or errors out. Used by the Excel exporters to label
// amount columns with the user's actual currency instead of a hard-coded
// "USD".
func (h *Handler) getBaseCurrency(ctx context.Context) string {
	setting, err := h.queries.GetSetting(ctx, SettingBaseCurrency)
	if err != nil || setting.Value == "" {
		return DefaultBaseCurrency
	}
	return setting.Value
}
