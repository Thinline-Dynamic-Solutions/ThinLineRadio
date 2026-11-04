/*
 * *****************************************************************************
 * Copyright (C) 2026 Thinline Dynamic Solutions
 *
 * Licensed under the GNU GPL v3 (or later). See LICENSE.
 * ****************************************************************************
 */

import { ChangeDetectionStrategy, ChangeDetectorRef, Component, OnDestroy, OnInit } from '@angular/core';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { MatSnackBar } from '@angular/material/snack-bar';
import { firstValueFrom } from 'rxjs';
import { RdioScannerService } from '../rdio-scanner.service';

interface DelayQueueCall {
  id: number;
  systemLabel: string;
  talkgroupLabel: string;
  talkgroupName: string;
  timestamp: number;
  releaseAt: number;
  transcript?: string;
}

@Component({
  selector: 'rdio-scanner-delay-queue-dialog',
  template: `
    <h2 mat-dialog-title>Delay Queue</h2>
    <mat-dialog-content>
      <p class="mat-body hint">
        Calls still being held back. Removing a call deletes it before any listener hears it,
        along with its transcript and alerts.
      </p>
      @if (loading && calls.length === 0) {
        <mat-progress-bar mode="indeterminate"></mat-progress-bar>
      } @else if (calls.length === 0) {
        <p class="mat-body empty">No calls are waiting.</p>
      } @else {
        @for (call of calls; track call.id) {
          <div class="row">
            <div class="row__main">
              <div class="row__title">{{ call.talkgroupLabel }} <span class="muted">· {{ call.systemLabel }}</span></div>
              <div class="row__meta muted">
                {{ call.timestamp | date: 'HH:mm:ss' }} · releases in {{ remaining(call) }}
              </div>
              @if (call.transcript) {
                <div class="row__transcript">{{ call.transcript }}</div>
              }
            </div>
            <button mat-icon-button (click)="togglePlay(call)" [attr.aria-label]="playingId === call.id ? 'Stop' : 'Preview'">
              <mat-icon>{{ playingId === call.id ? 'stop' : 'play_arrow' }}</mat-icon>
            </button>
            <button mat-icon-button color="warn" (click)="remove([call.id])" aria-label="Remove call">
              <mat-icon>delete</mat-icon>
            </button>
          </div>
        }
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button color="warn" [disabled]="calls.length === 0" (click)="removeAll()">Remove All</button>
      <button mat-button mat-dialog-close>Close</button>
    </mat-dialog-actions>
  `,
  styles: [`
    mat-dialog-content { min-width: 360px; max-height: 70vh; }
    .hint { margin-bottom: 12px; opacity: 0.8; }
    .empty { opacity: 0.7; }
    .muted { opacity: 0.65; }
    .row { display: flex; align-items: center; gap: 4px; padding: 8px 0; border-bottom: 1px solid rgba(127,127,127,0.2); }
    .row__main { flex: 1; min-width: 0; }
    .row__title { font-weight: 600; }
    .row__meta { font-size: 12px; }
    .row__transcript { font-size: 13px; margin-top: 4px; white-space: pre-wrap; }
  `],
  changeDetection: ChangeDetectionStrategy.OnPush,
  standalone: false,
})
export class RdioScannerDelayQueueDialogComponent implements OnInit, OnDestroy {
  calls: DelayQueueCall[] = [];
  loading = true;
  playingId: number | null = null;

  private clockSkew = 0;
  private refreshTimer?: ReturnType<typeof setInterval>;
  private tickTimer?: ReturnType<typeof setInterval>;
  private audio?: HTMLAudioElement;
  private audioUrl?: string;

  constructor(
    private http: HttpClient,
    private snack: MatSnackBar,
    private rdioScannerService: RdioScannerService,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    void this.reload();
    this.refreshTimer = setInterval(() => void this.reload(), 5000);
    this.tickTimer = setInterval(() => {
      const now = Date.now() + this.clockSkew;
      const before = this.calls.length;
      this.calls = this.calls.filter((c) => c.releaseAt > now);
      if (this.playingId !== null && !this.calls.some((c) => c.id === this.playingId)) {
        this.stopAudio();
      }
      if (before !== this.calls.length || this.calls.length > 0) {
        this.cdr.markForCheck();
      }
    }, 1000);
  }

  ngOnDestroy(): void {
    clearInterval(this.refreshTimer);
    clearInterval(this.tickTimer);
    this.stopAudio();
  }

  private authHeaders(): HttpHeaders {
    const pin = this.rdioScannerService.readPin();
    return pin ? new HttpHeaders({ Authorization: `Bearer ${pin}` }) : new HttpHeaders();
  }

  async reload(): Promise<void> {
    try {
      const res = await firstValueFrom(this.http.get<any>('/api/delay-queue', { headers: this.authHeaders() }));
      this.calls = Array.isArray(res?.calls) ? res.calls : [];
      if (typeof res?.serverTime === 'number') {
        this.clockSkew = res.serverTime - Date.now();
      }
    } catch (err: any) {
      this.snack.open(errorText(err, 'Failed to load the delay queue'), 'Close', { duration: 4000 });
      clearInterval(this.refreshTimer);
    } finally {
      this.loading = false;
      this.cdr.markForCheck();
    }
  }

  remaining(call: DelayQueueCall): string {
    const s = Math.max(0, Math.round((call.releaseAt - Date.now() - this.clockSkew) / 1000));
    return s >= 60 ? `${Math.floor(s / 60)}m ${s % 60}s` : `${s}s`;
  }

  async togglePlay(call: DelayQueueCall): Promise<void> {
    if (this.playingId === call.id) {
      this.stopAudio();
      return;
    }
    this.stopAudio();
    try {
      const blob = await firstValueFrom(this.http.get(`/api/delay-queue/audio?id=${call.id}`, {
        headers: this.authHeaders(),
        responseType: 'blob',
      }));
      this.audioUrl = URL.createObjectURL(blob);
      this.audio = new Audio(this.audioUrl);
      this.audio.onended = () => { this.stopAudio(); this.cdr.markForCheck(); };
      this.playingId = call.id;
      this.cdr.markForCheck();
      await this.audio.play();
    } catch {
      this.stopAudio();
      this.snack.open('Could not play this call. It may have just been released or removed.', 'Close', { duration: 4000 });
      void this.reload();
    }
  }

  private stopAudio(): void {
    this.audio?.pause();
    this.audio = undefined;
    if (this.audioUrl) {
      URL.revokeObjectURL(this.audioUrl);
      this.audioUrl = undefined;
    }
    this.playingId = null;
  }

  removeAll(): void {
    if (confirm(`Remove all ${this.calls.length} waiting call(s)? Nobody will hear them.`)) {
      void this.remove([], true);
    }
  }

  async remove(ids: number[], all = false): Promise<void> {
    try {
      const res = await firstValueFrom(this.http.post<any>('/api/delay-queue/remove', { ids, all }, { headers: this.authHeaders() }));
      const removed: number[] = Array.isArray(res?.removed) ? res.removed : [];
      if (this.playingId !== null && removed.includes(this.playingId)) {
        this.stopAudio();
      }
      this.calls = this.calls.filter((c) => !removed.includes(c.id));
      const missed = all ? 0 : ids.length - removed.length;
      this.snack.open(
        missed > 0 ? 'That call was already released.' : `Removed ${removed.length} call(s).`,
        'Close',
        { duration: 3000 },
      );
    } catch (err: any) {
      this.snack.open(errorText(err, 'Failed to remove'), 'Close', { duration: 4000 });
    } finally {
      this.cdr.markForCheck();
      void this.reload();
    }
  }
}

function errorText(err: any, fallback: string): string {
  const body = err?.error;
  return typeof body === 'string' && body.trim() && !body.trim().startsWith('<') ? body.trim() : fallback;
}
