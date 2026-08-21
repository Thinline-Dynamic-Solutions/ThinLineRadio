/*
 * *****************************************************************************
 * Copyright (C) 2025-2026 Thinline Dynamic Solutions
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 * ****************************************************************************
 */

import {
  AfterViewInit,
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  ElementRef,
  EventEmitter,
  Input,
  NgZone,
  OnDestroy,
  Output,
  ViewChild,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { MatButtonModule } from '@angular/material/button';
import { TurnstileScriptService } from './turnstile-script.service';

/**
 * Per-form Cloudflare Turnstile widget (issue #259).
 *
 * Each instance owns its own host element via ViewChild — never share a
 * global container ID across Angular *ngIf views. Destroy removes the
 * widget; expired / error / timeout remounts a fresh challenge.
 */
@Component({
  selector: 'rdio-scanner-turnstile',
  templateUrl: './turnstile.component.html',
  styleUrls: ['./turnstile.component.scss'],
  changeDetection: ChangeDetectionStrategy.OnPush,
  standalone: true,
  imports: [CommonModule, MatProgressSpinnerModule, MatButtonModule],
})
export class RdioScannerTurnstileComponent implements AfterViewInit, OnDestroy {
  @Input({ required: true }) siteKey!: string;
  /** Turnstile action passed to render + validated server-side. */
  @Input() action = '';
  @Input() theme: 'light' | 'dark' | 'auto' = 'light';

  @Output() tokenChange = new EventEmitter<string>();
  @Output() failed = new EventEmitter<string>();

  @ViewChild('host', { static: true }) host!: ElementRef<HTMLDivElement>;

  token = '';
  pending = true;
  loadError = '';

  private widgetId: string | null = null;
  private destroyed = false;
  private renderGeneration = 0;

  constructor(
    private script: TurnstileScriptService,
    private ngZone: NgZone,
    private cdr: ChangeDetectorRef
  ) {}

  ngAfterViewInit(): void {
    void this.mount();
  }

  ngOnDestroy(): void {
    this.destroyed = true;
    this.destroyWidget();
  }

  /** Clear used token and remount a new challenge (issue #239 / #259). */
  refresh(): void {
    this.setToken('');
    void this.mount(true);
  }

  retry(): void {
    this.loadError = '';
    this.failed.emit('');
    this.refresh();
  }

  private async mount(force = false): Promise<void> {
    if (this.destroyed || !this.siteKey) {
      return;
    }

    const gen = ++this.renderGeneration;
    this.pending = true;
    this.loadError = '';
    this.cdr.markForCheck();

    this.destroyWidget();

    try {
      await this.script.load();
    } catch {
      if (gen !== this.renderGeneration || this.destroyed) {
        return;
      }
      this.pending = false;
      this.loadError = 'Security check failed to load. Please retry.';
      this.failed.emit(this.loadError);
      this.cdr.markForCheck();
      return;
    }

    if (gen !== this.renderGeneration || this.destroyed) {
      return;
    }

    const el = this.host?.nativeElement;
    if (!el || !window.turnstile) {
      this.pending = false;
      this.loadError = 'Security check is unavailable. Please retry.';
      this.failed.emit(this.loadError);
      this.cdr.markForCheck();
      return;
    }

    el.innerHTML = '';

    try {
      const options: Record<string, unknown> = {
        sitekey: this.siteKey,
        theme: this.theme,
        size: 'normal',
        callback: (token: string) => {
          this.ngZone.run(() => {
            if (gen !== this.renderGeneration || this.destroyed) {
              return;
            }
            this.pending = false;
            this.loadError = '';
            this.setToken(token);
            this.cdr.markForCheck();
          });
        },
        'error-callback': () => {
          this.ngZone.run(() => {
            if (gen !== this.renderGeneration || this.destroyed) {
              return;
            }
            this.setToken('');
            this.pending = false;
            this.loadError = 'CAPTCHA verification failed. Please try again.';
            this.failed.emit(this.loadError);
            this.cdr.markForCheck();
          });
        },
        'expired-callback': () => {
          this.ngZone.run(() => {
            if (gen !== this.renderGeneration || this.destroyed) {
              return;
            }
            this.setToken('');
            // Remount so the form does not stay permanently disabled (#259).
            void this.mount(true);
          });
        },
        'timeout-callback': () => {
          this.ngZone.run(() => {
            if (gen !== this.renderGeneration || this.destroyed) {
              return;
            }
            this.setToken('');
            void this.mount(true);
          });
        },
      };
      if (this.action) {
        options['action'] = this.action;
      }

      this.widgetId = window.turnstile.render(el, options);
      this.pending = false;
      if (force) {
        // Widget rendered; token arrives via callback.
      }
      this.cdr.markForCheck();
    } catch {
      this.pending = false;
      this.loadError = 'Security check failed to render. Please retry.';
      this.failed.emit(this.loadError);
      this.cdr.markForCheck();
    }
  }

  private destroyWidget(): void {
    if (this.widgetId !== null && window.turnstile) {
      try {
        window.turnstile.remove(this.widgetId);
      } catch {
        // Ignore remove errors on destroyed DOM.
      }
    }
    this.widgetId = null;
    if (this.host?.nativeElement) {
      this.host.nativeElement.innerHTML = '';
    }
  }

  private setToken(token: string): void {
    this.token = token;
    this.tokenChange.emit(token);
  }
}
