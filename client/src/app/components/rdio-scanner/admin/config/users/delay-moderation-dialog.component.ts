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

import { ChangeDetectionStrategy, Component, Inject } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { System } from '../../admin.service';

interface ModeratedTalkgroup {
    ref: number;
    label: string;
    delay: number;
}

interface ModeratedSystem {
    ref: number;
    label: string;
    talkgroups: ModeratedTalkgroup[];
}

export interface DelayModerationDialogData {
    email: string;
    scope: string[];
    systems: System[];
    defaultDelay: number;
}

@Component({
    selector: 'rdio-scanner-delay-moderation-dialog',
    template: `
    <h2 mat-dialog-title>Delay Moderation</h2>
    <mat-dialog-content>
      <p class="mat-body intro">
        <strong>{{ data.email }}</strong> can preview calls on these talkgroups while they are
        still delayed, and remove them before any listener hears them. They only see
        talkgroups their own access allows.
      </p>
      @if (systems.length === 0) {
        <p class="mat-body muted">No talkgroups have a delay. Set a delay on a talkgroup or system first.</p>
      } @else {
        <mat-checkbox [(ngModel)]="all">All delayed talkgroups</mat-checkbox>
        @if (!all) {
          <mat-form-field appearance="outline" class="filter">
            <mat-label>Filter talkgroups</mat-label>
            <input matInput [(ngModel)]="filter" autocomplete="off">
          </mat-form-field>
          @for (system of systems; track system.ref) {
            @if (visibleTalkgroups(system).length > 0) {
              <div class="system">
                <mat-checkbox [checked]="wholeSystems.has(system.ref)" (change)="toggleSystem(system.ref, $event.checked)">
                  <strong>{{ system.label }}</strong> (whole system)
                </mat-checkbox>
                @if (!wholeSystems.has(system.ref)) {
                  <div class="talkgroups">
                    @for (tg of visibleTalkgroups(system); track tg.ref) {
                      <mat-checkbox [checked]="talkgroups.has(key(system.ref, tg.ref))" (change)="toggleTalkgroup(system.ref, tg.ref, $event.checked)">
                        {{ tg.label }} <span class="muted">· {{ tg.delay }} min</span>
                      </mat-checkbox>
                    }
                  </div>
                }
              </div>
            }
          }
        }
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button (click)="dialogRef.close()">Cancel</button>
      @if (data.scope.length > 0) {
        <button mat-button color="warn" (click)="dialogRef.close([])">Remove Moderator</button>
      }
      <button mat-raised-button color="primary" (click)="save()">Save</button>
    </mat-dialog-actions>
    `,
    styles: [`
    mat-dialog-content { min-width: 420px; max-height: 70vh; }
    .intro { margin-bottom: 12px; }
    .muted { opacity: 0.65; }
    .filter { width: 100%; margin-top: 12px; }
    .system { margin: 8px 0; }
    .talkgroups { display: flex; flex-direction: column; padding-left: 28px; }
  `],
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class DelayModerationDialogComponent {
    systems: ModeratedSystem[];
    all: boolean;
    filter = '';
    wholeSystems = new Set<number>();
    talkgroups = new Set<string>();

    constructor(
        public dialogRef: MatDialogRef<DelayModerationDialogComponent, string[]>,
        @Inject(MAT_DIALOG_DATA) public data: DelayModerationDialogData,
    ) {
        this.systems = delayedSystems(data.systems, data.defaultDelay);
        this.all = data.scope.includes('*');
        for (const entry of data.scope) {
            const [sys, tg] = entry.split(':');
            if (tg === '*') {
                this.wholeSystems.add(Number(sys));
            } else if (tg) {
                this.talkgroups.add(entry);
            }
        }
    }

    key(systemRef: number, talkgroupRef: number): string {
        return `${systemRef}:${talkgroupRef}`;
    }

    visibleTalkgroups(system: ModeratedSystem): ModeratedTalkgroup[] {
        const q = this.filter.trim().toLowerCase();
        if (!q || system.label.toLowerCase().includes(q)) {
            return system.talkgroups;
        }
        return system.talkgroups.filter((tg) => tg.label.toLowerCase().includes(q) || String(tg.ref).includes(q));
    }

    toggleSystem(systemRef: number, checked: boolean): void {
        if (checked) {
            this.wholeSystems.add(systemRef);
        } else {
            this.wholeSystems.delete(systemRef);
        }
    }

    toggleTalkgroup(systemRef: number, talkgroupRef: number, checked: boolean): void {
        const key = this.key(systemRef, talkgroupRef);
        if (checked) {
            this.talkgroups.add(key);
        } else {
            this.talkgroups.delete(key);
        }
    }

    save(): void {
        if (this.all) {
            this.dialogRef.close(['*']);
            return;
        }
        const scope = [...this.wholeSystems].map((ref) => `${ref}:*`);
        for (const key of this.talkgroups) {
            if (!this.wholeSystems.has(Number(key.split(':')[0]))) {
                scope.push(key);
            }
        }
        this.dialogRef.close(scope);
    }
}

function delayedSystems(systems: System[], defaultDelay: number): ModeratedSystem[] {
    const out: ModeratedSystem[] = [];
    for (const system of systems || []) {
        if (system.systemRef == null) {
            continue;
        }
        const talkgroups: ModeratedTalkgroup[] = [];
        for (const tg of system.talkgroups || []) {
            const delay = tg.delay || system.delay || defaultDelay || 0;
            if (tg.talkgroupRef != null && delay > 0) {
                talkgroups.push({ ref: tg.talkgroupRef, label: tg.label || tg.name || String(tg.talkgroupRef), delay });
            }
        }
        if (talkgroups.length > 0) {
            out.push({ ref: system.systemRef, label: system.label || String(system.systemRef), talkgroups });
        }
    }
    return out;
}
