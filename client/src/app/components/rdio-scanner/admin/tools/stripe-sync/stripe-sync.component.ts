import { Component, ChangeDetectionStrategy } from '@angular/core';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-stripe-sync',
    templateUrl: './stripe-sync.component.html',
    styleUrls: ['./stripe-sync.component.scss'],
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RdioScannerAdminStripeSyncComponent {
    syncing = false;
    syncResult: any = null;

    constructor(
        private adminService: RdioScannerAdminService,
        private snackBar: MatSnackBar
    ) {}

    async syncWithStripe(): Promise<void> {
        if (this.syncing) {
            return;
        }

        const confirmed = confirm(
            'This will fetch customer data from Stripe and update user records.\n\n' +
            'Users will be matched by email address.\n\n' +
            'Continue?'
        );

        if (!confirmed) {
            return;
        }

        this.syncing = true;
        this.syncResult = null;

        try {
            const result = await this.adminService.syncStripeCustomers();
            this.syncResult = result;
            
            if (result.success) {
                this.snackBar.open(
                    `Synced ${result.matched} users with Stripe data`,
                    'Close',
                    { duration: 5000 }
                );
            } else {
                this.snackBar.open(
                    result.error || 'Sync failed',
                    'Close',
                    { duration: 5000 }
                );
            }
        } catch (error: any) {
            this.snackBar.open(
                error?.error?.error || 'Failed to sync with Stripe',
                'Close',
                { duration: 5000 }
            );
        } finally {
            this.syncing = false;
        }
    }
}

