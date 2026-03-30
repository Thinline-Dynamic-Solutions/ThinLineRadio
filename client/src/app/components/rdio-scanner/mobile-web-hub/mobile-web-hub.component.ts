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

import { Component, EventEmitter, OnDestroy, OnInit, Output } from '@angular/core';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Subscription } from 'rxjs';
import { RdioScannerConfig } from '../rdio-scanner';
import { RdioScannerService } from '../rdio-scanner.service';

@Component({
    selector: 'rdio-scanner-mobile-web-hub',
    templateUrl: './mobile-web-hub.component.html',
    styleUrls: ['./mobile-web-hub.component.scss'],
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
                    this.loadingAccount = false;
                },
                error: () => {
                    this.loadingAccount = false;
                },
            });
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
        this.showCheckout = true;
        this.showChangeSubscription = false;
    }

    openChangeSubscription(): void {
        if (!this.accountInfo?.email) {
            this.snackBar.open('Unable to get your email address', 'Close', { duration: 3000 });
            return;
        }
        this.userEmail = this.accountInfo.email;
        this.showChangeSubscription = true;
        this.showCheckout = true;
    }

    onCheckoutSuccess(): void {
        this.showCheckout = false;
        this.showChangeSubscription = false;
        window.location.reload();
    }

    onCheckoutCancel(): void {
        this.showCheckout = false;
        this.showChangeSubscription = false;
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

    hasStoredPin(): boolean {
        return !!this.getPin();
    }
}
