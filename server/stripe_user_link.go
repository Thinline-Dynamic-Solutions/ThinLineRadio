// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"log"
	"strconv"
	"strings"

	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/customer"
	"github.com/stripe/stripe-go/v76/subscription"
)

// findUserForStripeCustomer resolves the local user a Stripe event belongs to.
// Stripe webhooks carry the customer as an unexpanded ID (no email), so when
// the customer ID no longer matches anyone this falls back to the subscription
// ID, the rdio_user_id metadata stamped at checkout, and the customer's email
// in Stripe, then re-links the customer ID onto the user (caller saves).
func (api *Api) findUserForStripeCustomer(customerID, subscriptionID string) *User {
	users := api.Controller.Users
	if customerID != "" {
		if u := users.GetUserByStripeCustomerId(customerID); u != nil {
			return u
		}
	}

	user := users.GetUserByStripeSubscriptionId(subscriptionID)
	if user == nil && api.Controller.Options.StripeSecretKey != "" {
		stripe.Key = api.Controller.Options.StripeSecretKey
		if subscriptionID != "" {
			if sub, err := subscription.Get(subscriptionID, nil); err == nil {
				user = api.userFromStripeMetadata(sub.Metadata)
			} else {
				log.Printf("Stripe user lookup: failed to fetch subscription %s: %v", subscriptionID, err)
			}
		}
		if user == nil && customerID != "" {
			if c, err := customer.Get(customerID, nil); err == nil {
				user = api.userFromStripeMetadata(c.Metadata)
				if user == nil && strings.TrimSpace(c.Email) != "" {
					// Only claim an email match when it can't steal another customer's user.
					if u := users.GetUserByEmail(NormalizeEmail(c.Email)); u != nil && (u.StripeCustomerId == "" || u.StripeCustomerId == customerID) {
						user = u
					}
				}
			} else {
				log.Printf("Stripe user lookup: failed to fetch customer %s: %v", customerID, err)
			}
		}
	}

	if user != nil && customerID != "" && user.StripeCustomerId != customerID {
		log.Printf("Re-linked Stripe customer %s to user %s (ID: %d), previous customer %q", customerID, user.Email, user.Id, user.StripeCustomerId)
		user.StripeCustomerId = customerID
	}
	return user
}

func (api *Api) userFromStripeMetadata(metadata map[string]string) *User {
	raw := strings.TrimSpace(metadata["rdio_user_id"])
	if raw == "" {
		return nil
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return nil
	}
	return api.Controller.Users.GetUserById(id)
}

// stripeCustomerIDsForUser returns the Stripe customers that may belong to the
// user: the linked one, or, when the link is missing, any customer with the
// user's email that isn't linked to a different user.
func (api *Api) stripeCustomerIDsForUser(user *User) ([]string, error) {
	if user.StripeCustomerId != "" {
		return []string{user.StripeCustomerId}, nil
	}
	stripe.Key = api.Controller.Options.StripeSecretKey
	ids := []string{}
	iter := customer.List(&stripe.CustomerListParams{Email: stripe.String(user.Email)})
	for iter.Next() {
		id := iter.Customer().ID
		if owner := api.Controller.Users.GetUserByStripeCustomerId(id); owner == nil || owner.Id == user.Id {
			ids = append(ids, id)
		}
	}
	return ids, iter.Err()
}

// stripeCustomerSubscriptions returns the customer's live subscription (active,
// trialing, past_due or unpaid), if any, and whether they have ever subscribed.
func (api *Api) stripeCustomerSubscriptions(customerID string) (live *stripe.Subscription, hadAny bool, err error) {
	stripe.Key = api.Controller.Options.StripeSecretKey
	iter := subscription.List(&stripe.SubscriptionListParams{
		Customer: stripe.String(customerID),
		Status:   stripe.String("all"),
	})
	for iter.Next() {
		sub := iter.Subscription()
		hadAny = true
		switch sub.Status {
		case stripe.SubscriptionStatusActive, stripe.SubscriptionStatusTrialing, stripe.SubscriptionStatusPastDue, stripe.SubscriptionStatusUnpaid:
			if live == nil {
				live = sub
			}
		}
	}
	return live, hadAny, iter.Err()
}

// relinkStripeSubscription points the user back at a subscription Stripe still
// bills but TLR lost track of, and restores PIN expiry and paid memberships.
func (api *Api) relinkStripeSubscription(user *User, sub *stripe.Subscription) error {
	if sub.Customer != nil && sub.Customer.ID != "" {
		user.StripeCustomerId = sub.Customer.ID
	}
	user.StripeSubscriptionId = sub.ID
	user.SubscriptionStatus = string(sub.Status)
	if sub.Status == stripe.SubscriptionStatusActive || sub.Status == stripe.SubscriptionStatusTrialing {
		if exp := api.calculatePinExpiration(sub, false); exp > 0 {
			user.PinExpiresAt = exp
		}
	}
	api.reconcilePaidMembershipsFromSubscription(user, sub)
	api.Controller.Users.Update(user)
	if err := api.Controller.Users.WriteSubscriptionState(user, api.Controller.Database); err != nil {
		return err
	}
	api.Controller.SyncConfigToFile()
	return nil
}
