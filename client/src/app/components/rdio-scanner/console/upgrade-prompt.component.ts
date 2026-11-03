/*
 * *****************************************************************************
 * Copyright (C) 2026 Thinline Dynamic Solutions
 *
 * Licensed under the GNU GPL v3 (or later). See LICENSE.
 * ****************************************************************************
 */

import { ChangeDetectionStrategy, ChangeDetectorRef, Component, EventEmitter, Input, OnChanges, Output } from '@angular/core';

const DISMISS_KEY = 'tlr.freeUpgrade.dismissed';

@Component({
  selector: 'rdio-scanner-upgrade-prompt',
  templateUrl: './upgrade-prompt.component.html',
  styleUrls: ['./upgrade-prompt.component.scss'],
  changeDetection: ChangeDetectionStrategy.OnPush,
  standalone: false,
})
export class RdioScannerUpgradePromptComponent implements OnChanges {
  @Input() visible = false;
  @Output() subscribe = new EventEmitter<void>();

  showModal = false;

  constructor(private cdr: ChangeDetectorRef) {}

  ngOnChanges(): void {
    if (!this.visible) {
      this.showModal = false;
      this.cdr.markForCheck();
      return;
    }
    const dismissed = sessionStorage.getItem(DISMISS_KEY) === '1';
    this.showModal = !dismissed;
    this.cdr.markForCheck();
  }

  dismiss(): void {
    sessionStorage.setItem(DISMISS_KEY, '1');
    this.showModal = false;
    this.cdr.markForCheck();
  }

  reopen(): void {
    this.showModal = true;
    this.cdr.markForCheck();
  }

  onSubscribe(): void {
    this.dismiss();
    this.subscribe.emit();
  }
}
