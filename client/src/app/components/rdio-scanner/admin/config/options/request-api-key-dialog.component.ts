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

import { Component, Inject, OnInit } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { FormBuilder, FormGroup, Validators, FormControl } from '@angular/forms';
import { HttpClient } from '@angular/common/http';
import { LocationDataService, Country, State, City, County } from 'src/app/services/location-data.service';

interface SelectedStateData {
  stateCode: string;
  stateName: string;
  counties: County[];
  selectedCounties: string[];
}

@Component({
  selector: 'rdio-scanner-request-api-key-dialog',
  template: `
    <h2 mat-dialog-title>{{ isUpdateMode ? 'Update' : 'Request' }} Push Notification API Key</h2>
    <mat-dialog-content>
      <!-- Domain Verification Step -->
      <div *ngIf="requiresDomainVerification && !apiKeyReceived">
        <div style="text-align: center; padding: 20px;">
          <mat-icon style="font-size: 48px; width: 48px; height: 48px; color: #2196F3; margin-bottom: 16px;">mail</mat-icon>
          <h3>Domain Verification Required</h3>
          <p style="margin: 16px 0;">A verification code has been sent to <strong>{{ verificationEmail }}</strong></p>
          <p style="color: #666; font-size: 14px; margin-bottom: 24px;">
            This domain already has a server registered. Please enter the verification code sent to the domain owner's email address.
          </p>
          
          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Verification Code</mat-label>
            <input matInput [formControl]="verificationCodeControl" placeholder="Enter 6-digit code" maxlength="6" style="text-align: center; font-size: 24px; letter-spacing: 8px;">
            <mat-hint>Enter the code sent to {{ verificationEmail }}</mat-hint>
          </mat-form-field>
          
          <div *ngIf="verificationError" class="error-message" style="margin-top: 16px;">
            {{ verificationError }}
          </div>
          
          <div style="display: flex; gap: 12px; justify-content: center; margin-top: 24px;">
            <button mat-button (click)="onCancel()">Cancel</button>
            <button mat-raised-button color="primary" (click)="onVerifyDomain()" [disabled]="!verificationCodeControl.value || verificationCodeControl.value.length !== 6 || verifying">
              <span *ngIf="!verifying">Verify</span>
              <span *ngIf="verifying">Verifying...</span>
            </button>
          </div>
        </div>
      </div>
      
      <!-- API Key Request Form -->
      <div *ngIf="!requiresDomainVerification && !apiKeyReceived">
        <form [formGroup]="apiKeyForm">
          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Server Name *</mat-label>
            <input matInput formControlName="serverName" required>
            <mat-error *ngIf="apiKeyForm.get('serverName')?.hasError('required')">
              Server name is required
            </mat-error>
          </mat-form-field>

          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Thinline Radio Server URL *</mat-label>
            <input matInput formControlName="serverURL" placeholder="https://test.thinlineds.com" required>
            <mat-hint>Your Thinline Radio server address</mat-hint>
            <mat-error *ngIf="apiKeyForm.get('serverURL')?.hasError('required')">
              Server URL is required
            </mat-error>
          </mat-form-field>

          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Contact Email *</mat-label>
            <input matInput type="email" formControlName="contactEmail" required>
            <mat-error *ngIf="apiKeyForm.get('contactEmail')?.hasError('required')">
              Contact email is required
            </mat-error>
            <mat-error *ngIf="apiKeyForm.get('contactEmail')?.hasError('email')">
              Please enter a valid email address
            </mat-error>
          </mat-form-field>

          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Country *</mat-label>
            <mat-select formControlName="country" (selectionChange)="onCountryChange()" [disabled]="countries.length === 0">
              <mat-option value="">-- Select Country --</mat-option>
              <mat-option *ngFor="let country of countries" [value]="country.iso2">
                {{ country.name }}
              </mat-option>
            </mat-select>
            <mat-hint *ngIf="countries.length === 0">
              Location data requires relay server URL to be configured
            </mat-hint>
            <mat-error *ngIf="apiKeyForm.get('country')?.hasError('required')">
              Country is required
            </mat-error>
          </mat-form-field>

          <!-- Multi-State Selector for US -->
          <div *ngIf="availableStates.length > 0 && apiKeyForm.get('country')?.value === 'US'">
            <div style="display: flex; gap: 12px; align-items: flex-start; margin-bottom: 16px;">
              <mat-form-field appearance="outline" style="flex: 1; margin-bottom: 0;">
                <mat-label>State/Province *</mat-label>
                <mat-select [(value)]="stateSelector">
                  <mat-option value="">-- Select State/Province --</mat-option>
                  <mat-option *ngFor="let state of availableStates" [value]="state.iso2">
                    {{ state.name }}
                  </mat-option>
                </mat-select>
              </mat-form-field>
              <button mat-raised-button color="primary" (click)="addState()" [disabled]="!stateSelector" type="button" style="margin-top: 8px;">
                Add State
              </button>
            </div>
            <p style="color: #666; font-size: 13px; margin-top: -8px; margin-bottom: 16px;">
              Select states and choose counties for each
            </p>

            <!-- Selected States with Counties -->
            <div *ngFor="let stateData of selectedStates; let i = index" 
                 style="background: #f5f5f5; border: 1px solid #e0e0e0; border-radius: 8px; padding: 16px; margin-bottom: 16px;">
              <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
                <h4 style="margin: 0; color: #2196F3; font-size: 16px;">{{ stateData.stateName }}</h4>
                <button mat-icon-button color="warn" (click)="removeState(i)" type="button">
                  <mat-icon>close</mat-icon>
                </button>
              </div>
              
              <mat-form-field appearance="outline" class="full-width" style="margin-bottom: 0;">
                <mat-label>Counties *</mat-label>
                <mat-select [(value)]="stateData.selectedCounties" multiple>
                  <mat-option *ngFor="let county of stateData.counties" [value]="county.name">
                    {{ county.name }}
                  </mat-option>
                </mat-select>
                <mat-hint>Select all counties your scanner covers in {{ stateData.stateName }}</mat-hint>
                <mat-error *ngIf="stateData.selectedCounties.length === 0">
                  At least one county is required
                </mat-error>
              </mat-form-field>
            </div>

            <div *ngIf="selectedStates.length === 0" style="color: #f44336; font-size: 14px; margin-top: -8px; margin-bottom: 16px;">
              Please add at least one state and select counties
            </div>
          </div>

          <!-- Single State Selector for non-US -->
          <mat-form-field appearance="outline" class="full-width" *ngIf="availableStates.length > 0 && apiKeyForm.get('country')?.value !== 'US'">
            <mat-label>State/Province *</mat-label>
            <mat-select formControlName="state" (selectionChange)="onStateChange()">
              <mat-option value="">-- Select State/Province --</mat-option>
              <mat-option *ngFor="let state of availableStates" [value]="state.iso2">
                {{ state.name }}
              </mat-option>
            </mat-select>
            <mat-error *ngIf="apiKeyForm.get('state')?.hasError('required')">
              State/Province is required
            </mat-error>
          </mat-form-field>

          <!-- Show city ONLY if counties are NOT available -->
          <mat-form-field appearance="outline" class="full-width" *ngIf="availableCounties.length === 0 && availableCities.length > 0">
            <mat-label>Cities *</mat-label>
            <mat-select formControlName="city" multiple>
              <mat-option *ngFor="let city of availableCities" [value]="city.name">
                {{ city.name }}
              </mat-option>
            </mat-select>
            <mat-hint>Select all cities your scanner covers</mat-hint>
            <mat-error *ngIf="apiKeyForm.get('city')?.hasError('required')">
              At least one city is required
            </mat-error>
          </mat-form-field>
          
          <!-- Manual city input if no counties and no cities available -->
          <mat-form-field appearance="outline" class="full-width" *ngIf="availableCounties.length === 0 && availableCities.length === 0 && apiKeyForm.get('state')?.value">
            <mat-label>Cities *</mat-label>
            <input matInput formControlName="city" placeholder="Enter city names (comma-separated)" (blur)="onManualCityInput()">
            <mat-hint>Enter all cities your scanner covers, separated by commas</mat-hint>
            <mat-error *ngIf="apiKeyForm.get('city')?.hasError('required')">
              At least one city is required
            </mat-error>
          </mat-form-field>

          <mat-checkbox formControlName="isPrivate" class="full-width">
            This is a private server
          </mat-checkbox>
          <p class="private-server-hint" *ngIf="apiKeyForm.get('isPrivate')?.value">
            <mat-icon>info</mat-icon>
            Your private server will not be shown in the mobile app's public server list.
          </p>

          <div style="margin-top: 16px; padding-top: 16px; border-top: 1px solid #e0e0e0;">
            <p style="margin-bottom: 12px; font-weight: 500;">Transcribed Alerts</p>
            <mat-radio-group formControlName="offersTranscribedAlerts" class="full-width">
              <mat-radio-button [value]="true" style="display: block; margin-bottom: 8px;">
                This server will offer transcribed alerts
              </mat-radio-button>
              <mat-radio-button [value]="false" style="display: block;">
                This server will not offer transcribed alerts
              </mat-radio-button>
            </mat-radio-group>
          </div>

          <div style="margin-top: 16px; padding-top: 16px; border-top: 1px solid #e0e0e0;">
            <mat-checkbox formControlName="notifyOnOffline" class="full-width">
              Notify me via email when this server goes offline
            </mat-checkbox>
            <p class="private-server-hint" *ngIf="apiKeyForm.get('notifyOnOffline')?.value">
              <mat-icon>info</mat-icon>
              You will receive up to 5 email notifications if your server goes offline. Notifications will stop once your server comes back online.
            </p>
          </div>

          <div *ngIf="errorMessage" class="error-message">
            {{ errorMessage }}
          </div>

          <div *ngIf="loading" class="loading">
            Requesting API key...
          </div>
        </form>
      </div>

      <div *ngIf="apiKeyReceived" class="api-key-result">
        <div class="success-message">
          <mat-icon color="primary">check_circle</mat-icon>
          API Key Created Successfully
        </div>
        <p style="margin: 20px 0; color: #666; line-height: 1.6;">
          Your API key has been generated and is ready to use. For security and to prevent cross-input between servers, API keys are not displayed after creation. Each server must have its own unique API key to ensure proper isolation and security.
        </p>
        <div class="info" style="background-color: #e7f3ff; border-left: 4px solid #2196F3; padding: 15px; margin: 20px 0; border-radius: 4px;">
          <p style="margin: 0; color: #0c5460; font-size: 14px;">
            <mat-icon style="vertical-align: middle; margin-right: 8px; font-size: 20px; width: 20px; height: 20px;">info</mat-icon>
            <strong>Need to retrieve your API key?</strong> Use the recovery feature in the admin panel with your server URL and email address to retrieve it securely.
          </p>
        </div>
        <p style="margin-top: 20px; color: #666; font-size: 13px; text-align: center;">
          This dialog will close automatically...
        </p>
      </div>
    </mat-dialog-content>
    <mat-dialog-actions>
      <button mat-button (click)="onCancel()">{{ apiKeyReceived ? 'Close' : 'Cancel' }}</button>
      <button *ngIf="!apiKeyReceived && !requiresDomainVerification" 
              mat-raised-button 
              color="primary" 
              [disabled]="apiKeyForm.invalid || loading" 
              (click)="onRequest()">
        {{ isUpdateMode ? 'Update' : 'Request' }} API Key
      </button>
    </mat-dialog-actions>
  `,
  styles: [`
    .full-width {
      width: 100%;
      margin-bottom: 16px;
    }
    mat-dialog-content {
      min-width: 500px;
      max-width: 600px;
      max-height: calc(90vh - 130px);
      overflow-y: auto;
      padding: 24px !important;
    }
    mat-dialog-title {
      margin: 0;
      padding: 24px 24px 16px 24px;
      font-size: 20px;
      font-weight: 500;
    }
    form {
      display: flex;
      flex-direction: column;
    }
    mat-form-field {
      display: block;
      width: 100%;
    }
    .error-message {
      color: #f44336;
      padding: 10px;
      background: #ffebee;
      border-radius: 4px;
      margin-top: 10px;
    }
    .loading {
      text-align: center;
      padding: 20px;
      color: #666;
    }
    .api-key-result {
      padding: 20px 0;
    }
    .success-message {
      display: flex;
      align-items: center;
      gap: 8px;
      color: #4caf50;
      margin-bottom: 20px;
      font-weight: 500;
    }
    .api-key-display {
      background: #f5f5f5;
      padding: 15px;
      border-radius: 4px;
      margin: 20px 0;
      position: relative;
    }
    .api-key-display label {
      display: block;
      font-size: 12px;
      color: #666;
      margin-bottom: 8px;
    }
    .api-key-value {
      font-family: monospace;
      font-size: 14px;
      word-break: break-all;
      padding-right: 40px;
    }
    .api-key-display button {
      position: absolute;
      top: 15px;
      right: 15px;
    }
    .warning {
      display: flex;
      align-items: center;
      gap: 8px;
      color: #ff9800;
      margin-top: 15px;
      font-size: 14px;
    }
    .warning mat-icon {
      font-size: 20px;
      width: 20px;
      height: 20px;
    }
    .security-warning {
      display: flex;
      align-items: center;
      gap: 8px;
      color: #f44336;
      margin-top: 10px;
      font-size: 14px;
      font-weight: 500;
    }
    .security-warning mat-icon {
      font-size: 20px;
      width: 20px;
      height: 20px;
    }
    .private-server-hint {
      display: flex;
      align-items: center;
      gap: 8px;
      color: #666;
      font-size: 13px;
      margin-top: -10px;
      margin-bottom: 10px;
      padding-left: 4px;
    }
    .private-server-hint mat-icon {
      font-size: 18px;
      width: 18px;
      height: 18px;
      color: #2196F3;
    }
  `]
})
export class RequestAPIKeyDialogComponent implements OnInit {
  apiKeyForm: FormGroup;
  loading = false;
  errorMessage = '';
  apiKeyReceived = false;
  authKey: string = '';
  countries: Country[] = [];
  availableStates: State[] = [];
  availableCounties: County[] = [];
  availableCities: City[] = [];
  isUpdateMode = false;
  existingAPIKey: string | null = null;
  requiresDomainVerification = false;
  verificationEmail = '';
  verificationCodeControl = new FormControl('', [Validators.required, Validators.minLength(6), Validators.maxLength(6)]);
  verificationError = '';
  verifying = false;
  pendingRequestData: any = null;
  
  // Multi-state selection
  selectedStates: SelectedStateData[] = [];
  stateSelector: string = '';

  constructor(
    public dialogRef: MatDialogRef<RequestAPIKeyDialogComponent>,
    @Inject(MAT_DIALOG_DATA) public data: { relayServerURL: string; existingAPIKey?: string | null },
    private fb: FormBuilder,
    private http: HttpClient,
    private locationService: LocationDataService
  ) {
    this.existingAPIKey = data.existingAPIKey || null;
    this.isUpdateMode = !!this.existingAPIKey;
    this.apiKeyForm = this.fb.group({
      serverName: ['', Validators.required],
      serverURL: ['', [Validators.required]],
      contactEmail: ['', [Validators.required, Validators.email]],
      state: ['', Validators.required],
      country: ['', Validators.required],
      county: [[]], // Array for multiple counties - validation set dynamically
      city: [[]], // Array for multiple cities - validation set dynamically
      isPrivate: [false],
      offersTranscribedAlerts: [null, Validators.required],
      notifyOnOffline: [false]
    });
  }

  async ngOnInit(): Promise<void> {
    // Set relay server URL for location service
    this.locationService.setRelayServerURL(this.data.relayServerURL);

    // Check if relay server is configured
    if (!this.locationService.isConfigured()) {
      this.errorMessage = 'Relay server URL not configured. Countries will not be available.';
      console.warn('Relay server URL not configured for location data');
      return;
    }

    // Load countries from relay server
    this.locationService.getAllCountries().subscribe({
      next: (countries: Country[]) => {
        console.log('Loaded countries:', countries.length);
        this.countries = countries;
        if (countries.length === 0) {
          this.errorMessage = 'No countries loaded. Please check your relay server configuration.';
        }
      },
      error: (error: any) => {
        console.error('Failed to load countries:', error);
        this.errorMessage = `Failed to load countries: ${error.message || 'Please check your relay server configuration'}`;
      }
    });

    // Compute hash using Web Crypto API (same algorithm as backend SHA256)
    // This matches the computation in both rdio-scanner and relay-server
    const seed = 'thinline-radio-relay-auth-2026';
    const encoder = new TextEncoder();
    const data = encoder.encode(seed);
    
    try {
      const hashBuffer = await crypto.subtle.digest('SHA-256', data);
      const hashArray = Array.from(new Uint8Array(hashBuffer));
      this.authKey = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
    } catch (error) {
      console.error('Failed to compute auth key:', error);
      this.errorMessage = 'Failed to initialize authorization. Please refresh the page.';
    }

    // If in update mode, load existing API key details
    if (this.isUpdateMode && this.existingAPIKey) {
      await this.loadExistingAPIKeyDetails();
    }
  }

  async loadExistingAPIKeyDetails(): Promise<void> {
    if (!this.existingAPIKey) return;

    this.loading = true;
    this.errorMessage = '';

    try {
      const headers: { [key: string]: string } = {
        'X-Rdio-Auth': this.authKey,
        'X-API-Key': this.existingAPIKey
      };

      const response: any = await this.http.get(`${this.data.relayServerURL}/api/keys/details`, { headers }).toPromise();

      if (response) {
        // Store the county/city values before loading location data
        const savedCounties = response.county 
          ? response.county.split(',').map((c: string) => c.trim()).filter((c: string) => c)
          : [];
        const savedCities = response.city
          ? response.city.split(',').map((c: string) => c.trim()).filter((c: string) => c)
          : [];

        // Pre-populate form with existing data (but not county/city yet)
        this.apiKeyForm.patchValue({
          serverName: response.server_name || '',
          serverURL: response.server_url || '',
          contactEmail: response.contact_email || '',
          state: response.state || '',
          country: response.country || '',
          isPrivate: response.is_private || false,
          offersTranscribedAlerts: response.offers_transcribed_alerts !== null ? response.offers_transcribed_alerts : null,
          notifyOnOffline: response.notify_on_offline || false
        });

        // Load location data first, then restore selections
        if (response.country) {
          const countryCode = response.country.toUpperCase();
          
          // Load states for the country
          this.locationService.getStatesForCountry(response.country).subscribe({
            next: async (states: State[]) => {
              this.availableStates = states;
              
              if (response.state && response.county) {
                if (countryCode === 'US') {
                  // Parse multi-state format: STATE1:county1,county2;STATE2:county3,county4
                  // Or old format: single state with comma-separated counties
                  this.selectedStates = [];
                  
                  if (response.county.includes(':')) {
                    // New multi-state format
                    const stateCountyPairs = response.county.split(';');
                    
                    for (const pair of stateCountyPairs) {
                      const [stateCode, countiesStr] = pair.split(':');
                      if (stateCode && countiesStr) {
                        try {
                          const counties = await this.locationService.getCounties(countryCode, stateCode).toPromise();
                          const stateName = states.find(s => s.iso2 === stateCode)?.name || stateCode;
                          const selectedCounties = countiesStr.split(',').map((c: string) => c.trim());
                          
                          this.selectedStates.push({
                            stateCode: stateCode,
                            stateName: stateName,
                            counties: counties || [],
                            selectedCounties: selectedCounties
                          });
                        } catch (error) {
                          console.error(`Error loading counties for ${stateCode}:`, error);
                        }
                      }
                    }
                  } else {
                    // Old format: single state
                    const stateCode = response.state.toUpperCase();
                    try {
                      const counties = await this.locationService.getCounties(countryCode, stateCode).toPromise();
                      const stateName = states.find(s => s.iso2 === stateCode)?.name || stateCode;
                      const selectedCounties = savedCounties;
                      
                      this.selectedStates.push({
                        stateCode: stateCode,
                        stateName: stateName,
                        counties: counties || [],
                        selectedCounties: selectedCounties
                      });
                    } catch (error) {
                      console.error(`Error loading counties for ${stateCode}:`, error);
                    }
                  }
                } else {
                  // Load cities for non-US
                  const stateCode = response.state.toUpperCase();
                  this.availableCounties = [];
                  this.locationService.getCities(countryCode, stateCode).subscribe({
                    next: (cities: City[]) => {
                      this.availableCities = cities;
                      
                      // Now set the city selections after dropdowns are populated
                      if (savedCities.length > 0) {
                        this.apiKeyForm.patchValue({ city: savedCities });
                      }
                    },
                    error: (error: any) => {
                      console.error('Failed to load cities:', error);
                      this.availableCities = [];
                    }
                  });
                }
              }
            },
            error: (error: any) => {
              console.error('Failed to load states:', error);
              this.availableStates = [];
            }
          });
        }
      }
    } catch (error: any) {
      console.error('Error loading existing API key details:', error);
      
      // Handle specific error cases
      if (error.status === 504 || error.name === 'TimeoutError') {
        this.errorMessage = 'Request timed out. The relay server may be slow or unreachable. You can still update your details manually.';
      } else if (error.status === 404) {
        this.errorMessage = 'API key not found on relay server. You may need to request a new key.';
        this.isUpdateMode = false;
      } else if (error.status === 401) {
        this.errorMessage = 'Authentication failed. Please check your API key.';
      } else {
        this.errorMessage = error.error?.error || error.message || 'Failed to load existing API key details. You can still update manually.';
      }
      
      // If we can't load details, allow manual editing (don't switch to request mode)
      // User can still fill out the form manually
    } finally {
      this.loading = false;
    }
  }

  // Update validation when country or state changes
  updateLocationValidation(): void {
    const countryCode = this.apiKeyForm.get('country')?.value;
    const countyControl = this.apiKeyForm.get('county');
    const cityControl = this.apiKeyForm.get('city');
    const stateControl = this.apiKeyForm.get('state');
    
    if (countryCode?.toUpperCase() === 'US') {
      // US uses multi-state selection, so we don't need form validators
      // Validation will be done manually in onRequest()
      countyControl?.clearValidators();
      cityControl?.clearValidators();
      stateControl?.clearValidators();
      cityControl?.setValue([], { emitEvent: false });
      countyControl?.setValue([], { emitEvent: false });
      stateControl?.setValue('', { emitEvent: false });
    } else if (countryCode) {
      // Other countries require state and at least one city
      stateControl?.setValidators([Validators.required]);
      cityControl?.setValidators([this.arrayNotEmptyValidator.bind(this)]);
      countyControl?.clearValidators();
      countyControl?.setValue([], { emitEvent: false });
    } else {
      // No country selected - clear both validators
      countyControl?.clearValidators();
      cityControl?.clearValidators();
      stateControl?.clearValidators();
    }
    
    countyControl?.updateValueAndValidity({ emitEvent: false });
    cityControl?.updateValueAndValidity({ emitEvent: false });
    stateControl?.updateValueAndValidity({ emitEvent: false });
  }

  // Validator to ensure array is not empty
  arrayNotEmptyValidator(control: any): any {
    if (!control) {
      return null;
    }
    const value = control.value;
    // Check if value is empty or an empty array
    if (!value || (Array.isArray(value) && value.length === 0)) {
      return { required: true };
    }
    // Also handle string case (for manual input)
    if (typeof value === 'string' && value.trim() === '') {
      return { required: true };
    }
    return null;
  }

  // Handle manual city input - convert comma-separated string to array
  onManualCityInput(): void {
    const cityControl = this.apiKeyForm.get('city');
    const value = cityControl?.value;
    
    // If it's a string (from manual input), convert to array
    if (typeof value === 'string' && value.trim()) {
      const cities = value.split(',').map((c: string) => c.trim()).filter((c: string) => c.length > 0);
      cityControl?.setValue(cities, { emitEvent: false });
    }
  }

  onCountryChange(): void {
    const countryCode = this.apiKeyForm.get('country')?.value;
    if (countryCode) {
      this.locationService.getStatesForCountry(countryCode).subscribe({
        next: (states: State[]) => {
          this.availableStates = states;
        },
        error: (error: any) => {
          console.error('Failed to load states:', error);
          this.availableStates = [];
        }
      });
    } else {
      this.availableStates = [];
    }
    // Reset state, county, and city when country changes
    this.apiKeyForm.patchValue({ state: '', county: [], city: [] });
    this.availableCounties = [];
    this.availableCities = [];
    
    // Clear multi-state selection when country changes
    this.selectedStates = [];
    this.stateSelector = '';
    
    this.updateLocationValidation();
  }

  addState(): void {
    const countryCode = this.apiKeyForm.get('country')?.value;
    if (!this.stateSelector || !countryCode) {
      return;
    }

    // Check if state already added
    if (this.selectedStates.find(s => s.stateCode === this.stateSelector)) {
      alert('This state has already been added');
      return;
    }

    // Find state name
    const stateName = this.availableStates.find(s => s.iso2 === this.stateSelector)?.name || this.stateSelector;

    // Load counties for this state
    this.locationService.getCounties(countryCode, this.stateSelector).subscribe({
      next: (counties: County[]) => {
        this.selectedStates.push({
          stateCode: this.stateSelector,
          stateName: stateName,
          counties: counties,
          selectedCounties: []
        });
        
        // Reset selector
        this.stateSelector = '';
      },
      error: (error: any) => {
        console.error('Failed to load counties:', error);
        alert('Failed to load counties for this state');
      }
    });
  }

  removeState(index: number): void {
    this.selectedStates.splice(index, 1);
  }

  onStateChange(): void {
    const countryCode = this.apiKeyForm.get('country')?.value;
    const stateCode = this.apiKeyForm.get('state')?.value;
    
    // Reset county and city when state changes
    this.apiKeyForm.patchValue({ county: [], city: [] });
    this.availableCounties = [];
    this.availableCities = [];
    
    if (countryCode && stateCode) {
      // Only US has county support - show counties only, no cities
      if (countryCode.toUpperCase() === 'US') {
        // Load counties for US - do NOT load cities
        this.locationService.getCounties(countryCode, stateCode).subscribe({
          next: (counties: County[]) => {
            this.availableCounties = counties;
            // Explicitly clear cities for US
            this.availableCities = [];
          },
          error: (error: any) => {
            console.error('Failed to load counties:', error);
            this.availableCounties = [];
            this.availableCities = [];
          }
        });
          } else {
            // For non-US countries, load cities only (no counties)
            this.availableCounties = [];
            this.locationService.getCities(countryCode, stateCode).subscribe({
              next: (cities: City[]) => {
                this.availableCities = cities;
              },
              error: (error: any) => {
                console.error('Failed to load cities:', error);
                this.availableCities = [];
              }
            });
          }
    }
    this.updateLocationValidation();
  }


  onCancel(): void {
    this.dialogRef.close(null);
  }

  async onRequest(): Promise<void> {
    const formValue = this.apiKeyForm.value;
    const countryCode = (formValue.country || '').toUpperCase();
    
    // For US, validate multi-state selection
    if (countryCode === 'US') {
      if (this.selectedStates.length === 0) {
        this.errorMessage = 'Please add at least one state';
        return;
      }
      
      // Validate that each state has counties selected
      for (const stateData of this.selectedStates) {
        if (stateData.selectedCounties.length === 0) {
          this.errorMessage = `Please select at least one county for ${stateData.stateName}`;
          return;
        }
      }
    } else {
      // For non-US, validate regular form
      if (this.apiKeyForm.invalid) {
        console.log('Form is invalid');
        Object.keys(this.apiKeyForm.controls).forEach(key => {
          const control = this.apiKeyForm.get(key);
          if (control && control.invalid) {
            console.log(`${key} is invalid:`, control.errors);
          }
        });
        return;
      }
    }

    this.loading = true;
    this.errorMessage = '';
    
    // Build state and county values based on country
    let stateValue = '';
    let countyValue = '';
    let cityValue = '';
    
    if (countryCode === 'US') {
      // Multi-state format: STATE1:county1,county2;STATE2:county3,county4
      const stateCountyPairs = this.selectedStates.map(stateData => 
        `${stateData.stateCode}:${stateData.selectedCounties.join(',')}`
      );
      
      stateValue = this.selectedStates.map(s => s.stateCode).join(';');
      countyValue = stateCountyPairs.join(';');
    } else {
      // Non-US: use regular form values
      stateValue = formValue.state || '';
      
      // Handle city value
      if (Array.isArray(formValue.city)) {
        cityValue = formValue.city.join(',');
      } else if (typeof formValue.city === 'string') {
        // Handle manual input - split and rejoin to normalize
        cityValue = formValue.city.split(',').map((c: string) => c.trim()).filter((c: string) => c).join(',');
      }
    }
    
    const payload = {
      server_name: formValue.serverName,
      server_url: formValue.serverURL,
      contact_email: formValue.contactEmail,
      state: stateValue,
      country: countryCode,
      county: countyValue,
      city: cityValue,
      is_private: formValue.isPrivate || false,
      offers_transcribed_alerts: formValue.offersTranscribedAlerts,
      notify_on_offline: formValue.notifyOnOffline || false
    };

    try {
      // Include the authorization key from backend
      if (!this.authKey) {
        this.errorMessage = 'Failed to get authorization key. Please try again.';
        this.loading = false;
        return;
      }

      const headers: { [key: string]: string } = {
        'Content-Type': 'application/json',
        'X-Rdio-Auth': this.authKey
      };

      // Add API key header if updating
      if (this.isUpdateMode && this.existingAPIKey) {
        headers['X-API-Key'] = this.existingAPIKey;
      }

      if (this.isUpdateMode) {
        // For updates, judge success purely on the HTTP status code.
        // The relay server may return 200, 204, or a body without `success`.
        // Using observe:'response' avoids body-parsing failures masking a real 2xx.
        const httpResp = await this.http
          .put(`${this.data.relayServerURL}/api/keys/update`, payload, { headers, observe: 'response' })
          .toPromise();

        if (httpResp && httpResp.status >= 200 && httpResp.status < 300) {
          this.dialogRef.close(this.existingAPIKey);
        } else {
          this.errorMessage = 'Update failed — please try again.';
        }
      } else {
        const response: any = await this.http
          .post(`${this.data.relayServerURL}/api/keys/request`, payload, { headers })
          .toPromise();

        // Check if domain verification is required
        if (response && response.requires_verification) {
          this.requiresDomainVerification = true;
          this.verificationEmail = response.email;
          this.pendingRequestData = payload;
          this.loading = false;
          return;
        }

        if (response && response.success) {
          this.apiKeyReceived = true;
          setTimeout(() => {
            this.dialogRef.close(response.api_key);
          }, 2000);
        } else {
          this.errorMessage = 'Failed to create API key on relay server';
        }
      }
    } catch (error: any) {
      console.error('Error requesting API key:', error);
      const message = error.error?.error || error.message || 'Failed to request API key';
      this.errorMessage = `Error: ${message}`;
    } finally {
      this.loading = false;
    }
  }

  async onVerifyDomain(): Promise<void> {
    if (!this.verificationCodeControl.value || this.verificationCodeControl.value.length !== 6) {
      this.verificationError = 'Please enter a 6-digit verification code';
      return;
    }

    this.verifying = true;
    this.verificationError = '';

    try {
      const headers: { [key: string]: string } = {
        'Content-Type': 'application/json',
        'X-Rdio-Auth': this.authKey
      };

      // Extract root domain from server URL
      const serverURL = this.pendingRequestData.server_url;
      let rootDomain = '';
      try {
        const url = new URL(serverURL);
        const hostname = url.hostname;
        const parts = hostname.split('.');
        if (parts.length >= 2) {
          // Handle common two-part TLDs
          const lastTwo = parts[parts.length - 2] + '.' + parts[parts.length - 1];
          const twoPartTLDs = ['co.uk', 'com.au', 'org.uk', 'net.au', 'co.nz', 'co.za', 'com.br', 'com.mx'];
          if (twoPartTLDs.includes(lastTwo) && parts.length >= 3) {
            rootDomain = parts.slice(-3).join('.');
          } else {
            rootDomain = parts.slice(-2).join('.');
          }
        } else {
          rootDomain = hostname;
        }
      } catch (e) {
        this.verificationError = 'Invalid server URL';
        this.verifying = false;
        return;
      }

      const verifyPayload = {
        root_domain: rootDomain,
        email: this.verificationEmail,
        code: this.verificationCodeControl.value
      };

      const response: any = await this.http.post(`${this.data.relayServerURL}/api/keys/verify-domain`, verifyPayload, { headers }).toPromise();

      if (response && response.success) {
        // API key created successfully - store it silently without displaying
        this.apiKeyReceived = true;
        this.requiresDomainVerification = false;
        const retrievedApiKey = response.api_key;
        // Close dialog and return API key to parent (stored silently, not displayed)
        setTimeout(() => {
          this.dialogRef.close(retrievedApiKey);
        }, 2000);
      } else {
        this.verificationError = 'Failed to verify domain. Please check your code and try again.';
      }
    } catch (error: any) {
      console.error('Error verifying domain:', error);
      this.verificationError = error.error?.error || error.message || 'Failed to verify domain code';
    } finally {
      this.verifying = false;
    }
  }


}

