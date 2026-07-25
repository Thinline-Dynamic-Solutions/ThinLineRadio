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

import { Component, Input, Output, EventEmitter, OnInit, OnDestroy } from '@angular/core';
import { RdioScannerConfig } from '../rdio-scanner';

declare var Stripe: any;

@Component({
  selector: 'rdio-scanner-stripe-checkout',
  templateUrl: './stripe-checkout.component.html',
  styleUrls: ['./stripe-checkout.component.scss']
})
export class RdioScannerStripeCheckoutComponent implements OnInit, OnDestroy {
  @Input() config!: RdioScannerConfig;
  @Input() email!: string;
  /** When set, used as Stripe success_url (e.g. post-verify flow). Otherwise defaults to /?checkout=success */
  @Input() customSuccessUrl: string | null = null;
  /** When set, used as Stripe cancel_url. Otherwise defaults to /?checkout=cancel */
  @Input() customCancelUrl: string | null = null;
  @Input() isChangingPlan: boolean = false;
  @Input() currentPriceId: string | null = null;
  /** When set, a combined multi-tier checkout is posted (one item per paid group)
   *  and the single-price selection UI is bypassed. */
  @Input() checkoutItems: { groupId: number; priceId: string }[] | null = null;
  @Output() checkoutSuccess = new EventEmitter<any>();
  @Output() checkoutError = new EventEmitter<any>();
  @Output() checkoutCancel = new EventEmitter<void>();

  stripe: any;
  elements: any;
  paymentElement: any;
  loading = false;
  error: string | null = null;
  selectedPriceId: string | null = null;

  ngOnInit(): void {
    this.initializeStripe();
  }

  ngOnDestroy(): void {
    if (this.elements) {
      this.elements.destroy();
    }
  }

  private initializeStripe(): void {
    // Auto-select first pricing option if available (but not the current plan)
    if (this.config.options?.pricingOptions && this.config.options.pricingOptions.length > 0) {
      for (const option of this.config.options.pricingOptions) {
        if (!this.currentPriceId || option.priceId !== this.currentPriceId) {
          this.selectedPriceId = option.priceId;
          break;
        }
      }
    }

    // For now, we'll skip the complex Stripe Elements setup
    // and just show a simple interface that redirects to Stripe Checkout
    console.log('Stripe configuration loaded successfully');
  }

  selectPrice(priceId: string): void {
    // Don't allow selecting the current plan
    if (this.currentPriceId && priceId === this.currentPriceId) {
      return;
    }
    this.selectedPriceId = priceId;
    this.error = null;
  }

  async handleSubmit(): Promise<void> {
    const combined = this.checkoutItems && this.checkoutItems.length > 0;
    const priceId = this.selectedPriceId;

    if (!combined && !priceId) {
      this.error = 'Please select a pricing option.';
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
      // Create checkout session on the backend
      const baseUrl = window.location.origin;
      const successUrl = this.customSuccessUrl && this.customSuccessUrl.length > 0
        ? this.customSuccessUrl
        : `${baseUrl}/?checkout=success`;
      const cancelUrl = this.customCancelUrl && this.customCancelUrl.length > 0
        ? this.customCancelUrl
        : `${baseUrl}/?checkout=cancel`;

      const body: any = { email: this.email, successUrl, cancelUrl };
      if (combined) {
        body.items = this.checkoutItems;
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
        console.log('Redirecting to Stripe Checkout:', result.checkoutUrl);
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
