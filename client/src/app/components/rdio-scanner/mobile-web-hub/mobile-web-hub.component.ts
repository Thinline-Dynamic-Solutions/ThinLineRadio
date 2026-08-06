/*
 * *****************************************************************************
 * Copyright (C) 2025 Thinline Dynamic Solutions
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 * ****************************************************************************
 */

import { Component, EventEmitter, OnDestroy, OnInit, Output, ChangeDetectionStrategy } from '@angular/core';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Subscription } from 'rxjs';
import { RdioScannerConfig } from '../rdio-scanner';
import { RdioScannerService } from '../rdio-scanner.service';

@Component({
    selector: 'rdio-scanner-mobile-web-hub',
    templateUrl: './mobile-web-hub.component.html',
    styleUrls: ['./mobile-web-hub.component.scss'],
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RdioScannerMobileWebHubComponent implements OnInit, OnDestroy {
    @Output() signOut = new EventEmitter<void>();

    branding = 'ThinLine Radio';
    userRegistrationEnabled = false;
    stripePaywallEnabled = false;

    config: RdioScannerConfig | null = null;
    accountInfo: any = null;
    loadingAccount = false;
    showCheckout = false;
    showChangeSubscription = false;
    userEmail = '';
    currentPriceId: string | null = null;
    checkoutItems: {
        groupId: number;
        priceId: string;
        name?: string;
        label?: string;
        amount?: string;
    }[] | null = null;
    addingTier = false;
    selectedTierPrice: { [groupId: number]: string } = {};
    selectedAvailableTierId: number | null = null;
    /** Price picks for unpaid memberships before Subscribe. */
    unpaidPriceSelection: { [groupId: number]: string } = {};

    isAndroid = false;
    isApple = false;

    private eventSub?: Subscription;

    constructor(
        private rdioScannerService: RdioScannerService,
        private http: HttpClient,
        private snackBar: MatSnackBar,
    ) {}

    ngOnInit(): void {
        const initial = (window as any)?.initialConfig;
        if (initial?.branding) {
            this.branding = initial.branding;
        }
        this.userRegistrationEnabled = !!initial?.options?.userRegistrationEnabled;
        this.stripePaywallEnabled = !!initial?.options?.stripePaywallEnabled;

        const ua = navigator.userAgent || '';
        this.isAndroid = /Android/i.test(ua);
        this.isApple = /iPhone|iPad|iPod/i.test(ua) || (navigator.platform === 'MacIntel' && (navigator as Navigator & { maxTouchPoints?: number }).maxTouchPoints! > 1);

        const existing = this.rdioScannerService.getConfig?.();
        if (existing) {
            this.config = existing;
        }
        this.eventSub = this.rdioScannerService.event.subscribe((event: any) => {
            if (event.config) {
                this.config = event.config;
            }
        });

        if (this.userRegistrationEnabled) {
            this.loadAccountInfo();
        }
    }

    ngOnDestroy(): void {
        this.eventSub?.unsubscribe();
    }

    onSignOutClick(): void {
        this.signOut.emit();
    }

    private getPin(): string | undefined {
        const pin = window?.localStorage?.getItem('rdio-scanner-pin');
        return pin ? window.atob(pin) : undefined;
    }

    private getAuthHeaders(): HttpHeaders {
        const pin = this.getPin();
        const headers = new HttpHeaders();
        if (pin) {
            return headers.set('Authorization', `Bearer ${pin}`);
        }
        return headers;
    }

    loadAccountInfo(): void {
        this.loadingAccount = true;
        const pin = this.getPin();
        if (!pin) {
            this.loadingAccount = false;
            return;
        }
        const headers = this.getAuthHeaders();
        this.http
            .get<any>('/api/account', {
                headers,
                params: { pin: encodeURIComponent(pin) },
            })
            .subscribe({
                next: (account) => {
                    this.accountInfo = account;
                    this.userEmail = account.email || '';
                    this.currentPriceId = account.currentPriceId || null;
                    this.seedUnpaidPriceDefaults(account);
                    this.loadingAccount = false;
                },
                error: () => {
                    this.loadingAccount = false;
                },
            });
    }

    private seedUnpaidPriceDefaults(account: any): void {
        const unpaid = account?.unpaidCheckoutItems || [];
        for (const u of unpaid) {
            if (!this.unpaidPriceSelection[u.groupId] && u.pricingOptions?.length) {
                this.unpaidPriceSelection[u.groupId] = u.pricingOptions[0].priceId;
            }
        }
        const available = account?.availableTiers || [];
        for (const t of available) {
            if (t.billingEnabled && t.pricingOptions?.length && !this.selectedTierPrice[t.groupId]) {
                this.selectedTierPrice[t.groupId] = t.pricingOptions[0].priceId;
            }
        }
        if (available.length && (this.selectedAvailableTierId == null
            || !available.some((t: any) => t.groupId === this.selectedAvailableTierId))) {
            this.selectedAvailableTierId = available[0].groupId;
        }
    }

    selectAvailableTier(groupId: number): void {
        this.selectedAvailableTierId = groupId;
        const t = (this.accountInfo?.availableTiers || []).find((x: any) => x.groupId === groupId);
        if (t?.billingEnabled && t.pricingOptions?.length && !this.selectedTierPrice[groupId]) {
            this.selectedTierPrice[groupId] = t.pricingOptions[0].priceId;
        }
    }

    setTierPrice(groupId: number, priceId: string): void {
        this.selectedAvailableTierId = groupId;
        this.selectedTierPrice[groupId] = priceId;
    }

    canAddSelectedTier(): boolean {
        if (this.addingTier || this.selectedAvailableTierId == null) {
            return false;
        }
        const t = (this.accountInfo?.availableTiers || []).find(
            (x: any) => x.groupId === this.selectedAvailableTierId
        );
        if (!t) {
            return false;
        }
        return !t.billingEnabled || !!this.selectedTierPrice[t.groupId];
    }

    addSelectedTier(): void {
        if (!this.canAddSelectedTier() || this.selectedAvailableTierId == null) {
            this.snackBar.open('Select one tier and a plan first', 'Close', { duration: 3000 });
            return;
        }
        this.addTier(this.selectedAvailableTierId, this.selectedTierPrice[this.selectedAvailableTierId]);
    }

    openBillingPortal(): void {
        const pin = this.getPin();
        if (!pin) {
            this.snackBar.open('Please log in to manage billing', 'Close', { duration: 3000 });
            return;
        }
        const headers = this.getAuthHeaders();
        const returnUrl = window.location.href;
        this.http
            .post<any>(
                '/api/billing/portal',
                { returnUrl },
                {
                    headers,
                    params: { pin: encodeURIComponent(pin) },
                },
            )
            .subscribe({
                next: (response) => {
                    if (response.url) {
                        window.location.href = response.url;
                    } else {
                        this.snackBar.open('Failed to open billing portal', 'Close', { duration: 3000 });
                    }
                },
                error: (error) => {
                    const message = error.error?.error || 'Failed to open billing portal';
                    this.snackBar.open(message, 'Close', { duration: 5000 });
                },
            });
    }

    openCheckout(): void {
        if (!this.accountInfo?.email) {
            this.snackBar.open('Unable to get your email address', 'Close', { duration: 3000 });
            return;
        }
        this.userEmail = this.accountInfo.email;
        this.checkoutItems = this.buildUnpaidCheckoutItems();
        if (!this.checkoutItems?.length && !(this.accountInfo.pricingOptions?.length > 0)) {
            this.snackBar.open('No unpaid plans available to subscribe. Contact support if this persists.', 'Close', { duration: 5000 });
            return;
        }
        this.showCheckout = true;
        this.showChangeSubscription = false;
    }

    openChangeSubscription(): void {
        if (!this.accountInfo?.email) {
            this.snackBar.open('Unable to get your email address', 'Close', { duration: 3000 });
            return;
        }
        this.userEmail = this.accountInfo.email;
        this.checkoutItems = null;
        this.showChangeSubscription = true;
        this.showCheckout = true;
    }

    private buildUnpaidCheckoutItems(): {
        groupId: number;
        priceId: string;
        name?: string;
        label?: string;
        amount?: string;
    }[] | null {
        const unpaid = this.accountInfo?.unpaidCheckoutItems as any[] | undefined;
        const memberships = this.accountInfo?.memberships as any[] | undefined;
        const sources = (unpaid?.length ? unpaid : memberships?.filter((m: any) =>
            m.billingEnabled && m.selfBillable && !m.paid && m.pricingOptions?.length
        )) || [];
        if (!sources.length) {
            return null;
        }
        const items: {
            groupId: number;
            priceId: string;
            name?: string;
            label?: string;
            amount?: string;
        }[] = [];
        for (const src of sources) {
            const opts = src.pricingOptions || [];
            const selectedId = this.unpaidPriceSelection[src.groupId];
            const opt = opts.find((o: any) => o.priceId === selectedId) || opts[0];
            if (!opt?.priceId) {
                continue;
            }
            items.push({
                groupId: src.groupId,
                priceId: opt.priceId,
                name: src.name,
                label: opt.label,
                amount: opt.amount,
            });
        }
        return items.length ? items : null;
    }

    addTier(groupId: number, priceId?: string): void {
        const pin = this.getPin();
        if (!pin) {
            this.snackBar.open('Please log in to manage your subscription', 'Close', { duration: 3000 });
            return;
        }
        this.addingTier = true;
        const headers = this.getAuthHeaders();
        this.http.post<any>('/api/subscription/groups/add', { groupId, priceId: priceId || '' }, {
            headers,
            params: { pin: encodeURIComponent(pin) },
        }).subscribe({
            next: (res) => {
                this.addingTier = false;
                if (res.added) {
                    this.snackBar.open('Tier added to your subscription', 'Close', { duration: 3000 });
                    this.loadAccountInfo();
                } else if (res.needsCheckout && priceId) {
                    if (!this.config) {
                        const existing = this.rdioScannerService.getConfig?.();
                        if (existing) {
                            this.config = existing;
                        }
                    }
                    if (!this.config?.options?.stripePublishableKey) {
                        this.snackBar.open(
                            'Billing is still loading. Wait a moment and try again.',
                            'Close',
                            { duration: 5000 }
                        );
                        return;
                    }
                    this.userEmail = this.accountInfo?.email || res.email || '';
                    const tier = (this.accountInfo?.availableTiers || []).find((t: any) => t.groupId === groupId);
                    const opt = (tier?.pricingOptions || []).find((o: any) => o.priceId === priceId);
                    this.checkoutItems = [{
                        groupId,
                        priceId,
                        name: tier?.name,
                        label: opt?.label,
                        amount: opt?.amount,
                    }];
                    this.showChangeSubscription = false;
                    this.showCheckout = true;
                }
            },
            error: (error) => {
                this.addingTier = false;
                this.snackBar.open(error.error?.error || 'Failed to add tier', 'Close', { duration: 5000 });
            },
        });
    }

    removeTier(groupId: number): void {
        if (!confirm('Remove this tier? You will lose access to its channels.')) {
            return;
        }
        const pin = this.getPin();
        if (!pin) {
            return;
        }
        const headers = this.getAuthHeaders();
        this.http.post<any>('/api/subscription/groups/remove', { groupId }, {
            headers,
            params: { pin: encodeURIComponent(pin) },
        }).subscribe({
            next: () => {
                this.snackBar.open('Tier removed', 'Close', { duration: 3000 });
                this.loadAccountInfo();
            },
            error: (error) => {
                this.snackBar.open(error.error?.error || 'Failed to remove tier', 'Close', { duration: 5000 });
            },
        });
    }

    onCheckoutSuccess(): void {
        this.showCheckout = false;
        this.showChangeSubscription = false;
        this.checkoutItems = null;
        window.location.reload();
    }

    onCheckoutCancel(): void {
        this.showCheckout = false;
        this.showChangeSubscription = false;
        this.checkoutItems = null;
    }

    isGroupAdminManaged(): boolean {
        if (!this.accountInfo) {
            return false;
        }
        const status = this.accountInfo.subscriptionStatusDisplay || this.accountInfo.subscriptionStatus;
        return (
            status === 'group_admin_managed' ||
            (this.accountInfo.billingRequired &&
                !this.accountInfo.isGroupAdmin &&
                this.accountInfo.subscriptionStatus === 'group_admin_managed')
        );
    }

    showBillingSection(): boolean {
        return this.userRegistrationEnabled && this.stripePaywallEnabled;
    }

    showSubscribeButton(): boolean {
        if (!this.accountInfo || this.isGroupAdminManaged()) {
            return false;
        }
        const active = this.accountInfo.subscriptionStatus === 'active'
            || this.accountInfo.subscriptionStatus === 'trialing';
        if (active && this.accountInfo.stripeSubscriptionId) {
            return false;
        }
        return !!(this.accountInfo.billingRequired
            || this.accountInfo.pinExpired
            || (this.accountInfo.unpaidCheckoutItems?.length > 0));
    }

    hasStoredPin(): boolean {
        return !!this.getPin();
    }

    unpaidSources(): any[] {
        return this.accountInfo?.unpaidCheckoutItems
            || (this.accountInfo?.memberships || []).filter((m: any) =>
                m.billingEnabled && m.selfBillable && !m.paid && m.pricingOptions?.length);
    }
}
