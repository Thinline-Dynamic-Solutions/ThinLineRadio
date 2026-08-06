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
import { ActivatedRoute } from '@angular/router';
import { HttpClient } from '@angular/common/http';

@Component({
    selector: 'rdio-scanner-post-verify-welcome',
    templateUrl: './post-verify-welcome.component.html',
    styleUrls: ['./post-verify-welcome.component.scss'],
    standalone: false
})
export class RdioScannerPostVerifyWelcomeComponent implements OnInit {
  email = '';
  fromCheckout = false;
  skippedBilling = false;
  branding = 'ThinLine Radio';

  playStoreUrl = 'https://play.google.com/store/apps/details?id=com.thinlinedynamicsolutions.ohiorsn';
  appStoreUrl = 'https://apps.apple.com/us/app/ohiorsn/id6740734031';

  constructor(
    private readonly route: ActivatedRoute,
    private readonly http: HttpClient
  ) {}

  ngOnInit(): void {
    const ic = (window as any).initialConfig;
    if (ic?.branding) {
      this.branding = ic.branding;
    }
    const opt = ic?.options;
    if (opt?.androidPlayStoreUrl) {
      this.playStoreUrl = opt.androidPlayStoreUrl;
    }
    if (opt?.iosAppStoreUrl) {
      this.appStoreUrl = opt.iosAppStoreUrl;
    }
    this.http.get<{ iosAppStoreUrl?: string; androidPlayStoreUrl?: string }>('/api/public-app-links').subscribe({
      next: (r) => {
        if (r.androidPlayStoreUrl) {
          this.playStoreUrl = r.androidPlayStoreUrl;
        }
        if (r.iosAppStoreUrl) {
          this.appStoreUrl = r.iosAppStoreUrl;
        }
      },
      error: () => { /* keep initialConfig or defaults */ }
    });
    this.route.queryParams.subscribe(params => {
      this.email = (params['email'] || '').trim();
      this.fromCheckout = params['from'] === 'checkout';
      this.skippedBilling = params['skipped'] === '1';
    });
  }
}
