/*
 * ThinLine Radio — LCD bottom navigation bar
 *
 * Web parity for `lib/widgets/lcd_bottom_nav_bar.dart`. Same API surface:
 *   - items: array of { icon, label }
 *   - selectedIndex: currently active tab
 *   - (selectedIndexChange): emitted on tap
 *
 * Visual rules mirror the latest mobile tweak (issue #199 follow-up):
 *   inactive tabs use --tlr-nav-inactive (#9bbfaf dark / #2f4338 light) for
 *   readability, icons are 20px, labels 9px / weight 600, active uses
 *   --tlr-primary with neon glow.
 */
import {
    ChangeDetectionStrategy,
    Component,
    EventEmitter,
    Input,
    Output,
} from '@angular/core';

export interface LcdNavItem {
    /** Material icon ligature (e.g. "radio", "play_arrow"). */
    icon: string;
    /** Short uppercase label shown under the icon. */
    label: string;
    /** Optional value forwarded back on selection — falls back to index. */
    value?: string | number;
}

@Component({
    selector: 'rdio-scanner-lcd-bottom-nav',
    templateUrl: './lcd-bottom-nav.component.html',
    styleUrls: ['./lcd-bottom-nav.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
})
export class RdioScannerLcdBottomNavComponent {
    @Input() items: LcdNavItem[] = [];
    @Input() selectedIndex = 0;

    @Output() selectedIndexChange = new EventEmitter<number>();
    @Output() itemSelected = new EventEmitter<LcdNavItem>();

    onSelect(index: number): void {
        if (index === this.selectedIndex) return;
        this.selectedIndex = index;
        this.selectedIndexChange.emit(index);
        const item = this.items[index];
        if (item) this.itemSelected.emit(item);
    }

    trackByIndex(index: number): number {
        return index;
    }
}
