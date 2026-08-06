/*
 * *****************************************************************************
 * Copyright (C) 2025 Thinline Dynamic Solutions
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>
 * ****************************************************************************
 */

import { Component, Input, Output, EventEmitter, OnInit, OnDestroy, OnChanges, SimpleChanges } from '@angular/core';
import { RdioScannerConfig } from '../rdio-scanner';

export interface StripeCheckoutTier {
    groupId: number;
    name: string;
    description?: string;
    pricingOptions: Array<{
        priceId: string;
        label: string;
        amount: string;
        trialDays?: number;
    }>;
    /** Unpaid membership — must stay selected. */
    required?: boolean;
    /** Pre-select this tier in the picker. */
    included?: boolean;
}

interface TierChoice extends StripeCheckoutTier {
    included: boolean;
    priceId: string | null;
}

@Component({
    selector: 'rdio-scanner-stripe-checkout',
    templateUrl: './stripe-checkout.component.html',
    styleUrls: ['./stripe-checkout.component.scss']
})
export class RdioScannerStripeCheckoutComponent implements OnInit, OnDestroy, OnChanges {
    @Input() config!: RdioScannerConfig;
    @Input() email!: string;
    /** When set, used as Stripe success_url (e.g. post-verify flow). Otherwise defaults to /?checkout=success */
    @Input() customSuccessUrl: string | null = null;
    /** When set, used as Stripe cancel_url. Otherwise defaults to /?checkout=cancel */
    @Input() customCancelUrl: string | null = null;
    @Input() isChangingPlan: boolean = false;
    @Input() currentPriceId: string | null = null;
    /** When set, a combined multi-tier checkout is posted (one item per paid group)
     *  and the single-price selection UI is bypassed. Optional name/label/amount are display-only. */
    @Input() checkoutItems: {
        groupId: number;
        priceId: string;
        name?: string;
        label?: string;
        amount?: string;
    }[] | null = null;
    /**
     * Interactive multi-tier picker: unpaid memberships + other public tiers.
     * Takes precedence over flat pricingOptions when non-empty.
     */
    @Input() checkoutTiers: StripeCheckoutTier[] | null = null;
    @Output() checkoutSuccess = new EventEmitter<any>();
    @Output() checkoutError = new EventEmitter<any>();
    @Output() checkoutCancel = new EventEmitter<void>();

    stripe: any;
    elements: any;
    paymentElement: any;
    loading = false;
    error: string | null = null;
    selectedPriceId: string | null = null;
    tierChoices: TierChoice[] = [];

    ngOnInit(): void {
        this.syncTierFromInputs();
        if (this.checkoutItems && this.checkoutItems.length > 0) {
            this.selectedPriceId = this.checkoutItems[0].priceId;
            return;
        }
        if (this.tierChoices.length > 0) {
            return;
        }
        this.initializeStripe();
    }

    ngOnChanges(changes: SimpleChanges): void {
        if (changes['checkoutTiers'] || changes['checkoutItems']) {
            this.syncTierFromInputs();
        }
    }

    ngOnDestroy(): void {
        if (this.elements) {
            this.elements.destroy();
        }
    }

    get usesTierPicker(): boolean {
        return this.tierChoices.length > 0 && !(this.checkoutItems && this.checkoutItems.length > 0);
    }

    private syncTierFromInputs(): void {
        const tiers = this.checkoutTiers || [];
        this.tierChoices = tiers
            .filter((t) => t?.groupId && t.pricingOptions?.length)
            .map((t) => ({
                ...t,
                required: !!t.required,
                included: false,
                priceId: null,
            }));

        // Default: one plan only — prefer first unpaid/required tier, else first tier.
        const preferred =
            this.tierChoices.find((t) => t.required) || this.tierChoices[0];
        if (preferred?.pricingOptions?.length) {
            preferred.included = true;
            preferred.priceId = preferred.pricingOptions[0].priceId || null;
        }
    }

    private initializeStripe(): void {
        if (this.config.options?.pricingOptions && this.config.options.pricingOptions.length > 0) {
            for (const option of this.config.options.pricingOptions) {
                if (!this.currentPriceId || option.priceId !== this.currentPriceId) {
                    this.selectedPriceId = option.priceId;
                    break;
                }
            }
        }
    }

    isTierSelected(tier: TierChoice): boolean {
        return !!tier.included && !!tier.priceId;
    }

    isPlanSelected(tier: TierChoice, priceId: string): boolean {
        return !!tier.included && tier.priceId === priceId;
    }

    /** Single-select: choosing a plan clears every other tier. */
    selectTierPrice(tier: TierChoice, priceId: string): void {
        for (const t of this.tierChoices) {
            if (t.groupId === tier.groupId) {
                t.included = true;
                t.priceId = priceId;
            } else {
                t.included = false;
                t.priceId = null;
            }
        }
        this.error = null;
    }

    selectPrice(priceId: string): void {
        if (this.currentPriceId && priceId === this.currentPriceId) {
            return;
        }
        this.selectedPriceId = priceId;
        this.error = null;
    }

    private buildItemsFromTiers(): {
        groupId: number;
        priceId: string;
        name?: string;
        label?: string;
        amount?: string;
    }[] {
        const items: {
            groupId: number;
            priceId: string;
            name?: string;
            label?: string;
            amount?: string;
        }[] = [];
        for (const t of this.tierChoices) {
            if (!t.included || !t.priceId) {
                continue;
            }
            const opt = t.pricingOptions.find((o) => o.priceId === t.priceId);
            items.push({
                groupId: t.groupId,
                priceId: t.priceId,
                name: t.name,
                label: opt?.label,
                amount: opt?.amount,
            });
        }
        return items;
    }

    canSubmit(): boolean {
        if (this.loading) {
            return false;
        }
        if (this.checkoutItems?.length) {
            return true;
        }
        if (this.usesTierPicker) {
            return this.buildItemsFromTiers().length > 0;
        }
        return !!this.selectedPriceId;
    }

    async handleSubmit(): Promise<void> {
        const combinedPrebuilt = this.checkoutItems && this.checkoutItems.length > 0;
        const tierItems = this.usesTierPicker ? this.buildItemsFromTiers() : [];
        const combined = combinedPrebuilt || tierItems.length > 0;
        const priceId = this.selectedPriceId;

        if (!combined && !priceId) {
            this.error = 'Please select a pricing option.';
            return;
        }
        if (this.usesTierPicker && tierItems.length === 0) {
            this.error = 'Select a plan to subscribe.';
            return;
        }

        const publishableKey = this.config.options?.stripePublishableKey;
        if (!publishableKey) {
            this.error = 'Stripe configuration is missing. Please contact support.';
            return;
        }

        this.loading = true;
        this.error = null;

        try {
            const baseUrl = window.location.origin;
            const successUrl = this.customSuccessUrl && this.customSuccessUrl.length > 0
                ? this.customSuccessUrl
                : `${baseUrl}/?checkout=success`;
            const cancelUrl = this.customCancelUrl && this.customCancelUrl.length > 0
                ? this.customCancelUrl
                : `${baseUrl}/?checkout=cancel`;

            const body: any = { email: this.email, successUrl, cancelUrl };
            if (combinedPrebuilt) {
                body.items = this.checkoutItems;
            } else if (tierItems.length > 0) {
                body.items = tierItems;
            } else {
                body.priceId = priceId;
            }

            const response = await fetch('/api/stripe/create-checkout-session', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(body)
            });

            const result = await response.json();

            if (result.error) {
                this.error = result.error;
                this.loading = false;
                return;
            }

            if (result.checkoutUrl) {
                window.location.href = result.checkoutUrl;
            } else {
                this.error = 'No checkout URL received from server. Please contact support.';
                this.loading = false;
            }

        } catch (err: any) {
            this.error = err.message || 'An error occurred during checkout.';
            this.checkoutError.emit(err);
            this.loading = false;
        }
    }

    onCancel(): void {
        this.checkoutCancel.emit();
    }

    getBranding(): string {
        return this.config.branding || 'ThinLine Radio';
    }
}
