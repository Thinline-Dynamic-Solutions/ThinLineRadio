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

import { Component, OnInit } from '@angular/core';
import { ActivatedRoute, Router } from '@angular/router';
import { HttpClient } from '@angular/common/http';
import { RdioScannerConfig } from '../rdio-scanner';

@Component({
    selector: 'rdio-scanner-post-verify-plan',
    templateUrl: './post-verify-plan.component.html',
    styleUrls: ['./post-verify-plan.component.scss'],
    standalone: false
})
export class RdioScannerPostVerifyPlanComponent implements OnInit {
  loading = true;
  error = '';
  email = '';
  checkoutConfig: RdioScannerConfig | null = null;
  checkoutSuccessUrl = '';
  checkoutCancelUrl = '';

  constructor(
    private readonly route: ActivatedRoute,
    private readonly router: Router,
    private readonly http: HttpClient
  ) {}

  ngOnInit(): void {
    this.route.queryParams.subscribe(params => {
      this.email = (params['email'] || '').trim().toLowerCase();
      if (!this.email) {
        this.loading = false;
        this.error = 'Missing email. Open the verification link from your email again.';
        return;
      }
      this.loadContext();
    });
  }

  private loadContext(): void {
    this.loading = true;
    this.error = '';
    const url = `/api/user/post-verify-plan-context?email=${encodeURIComponent(this.email)}`;
    this.http.get<any>(url).subscribe({
      next: (ctx) => {
        if (!ctx.requiresPlanSelection) {
          this.router.navigate(['/setup/welcome'], { queryParams: { email: ctx.email || this.email } });
          return;
        }
        this.checkoutConfig = {
          branding: ctx.branding || 'ThinLine Radio',
          email: ctx.email || this.email,
          options: {
            pricingOptions: ctx.pricingOptions || [],
            stripePublishableKey: ctx.stripePublishableKey || ''
          }
        } as RdioScannerConfig;
        const origin = typeof window !== 'undefined' ? window.location.origin : '';
        const em = encodeURIComponent(ctx.email || this.email);
        this.checkoutSuccessUrl = `${origin}/setup/welcome?email=${em}&from=checkout`;
        this.checkoutCancelUrl = `${origin}/setup/plan?email=${em}`;
        this.loading = false;
      },
      error: (err) => {
        this.loading = false;
        this.error = err.error?.message || err.error?.error || 'Unable to load billing options.';
      }
    });
  }

  onCheckoutCancel(): void {
    this.router.navigate(['/setup/welcome'], { queryParams: { email: this.email, skipped: '1' } });
  }
}
