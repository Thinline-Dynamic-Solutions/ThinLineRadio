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

import { Component, OnInit, ChangeDetectorRef, Input, OnChanges, SimpleChanges } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { MatSnackBar } from '@angular/material/snack-bar';
import { FormBuilder, FormGroup, FormArray, Validators } from '@angular/forms';
import { RdioScannerAdminService, System } from '../../admin.service';

interface PricingOption {
  priceId: string;
  label: string;
  amount: string;
  trialDays?: number; // Optional: 0 = no trial, 1-30 = trial days
}

interface UserGroup {
  id: number;
  name: string;
  description: string;
  systemAccess: string;
  delay: number;
  systemDelays: string;
  talkgroupDelays: string;
  connectionLimit: number;
  maxUsers: number;
  billingEnabled: boolean;
  stripePriceId: string;
  pricingOptions?: PricingOption[];
  billingMode: string;
  collectSalesTax?: boolean;
  taxMode?: string;
  stripeTaxRateId?: string;
  isPublicRegistration: boolean;
  allowAddExistingUsers: boolean;
  createdAt: number;
}

interface RegistrationCode {
  id: number;
  code: string;
  expiresAt: number;
  maxUses: number;
  currentUses: number;
  isOneTime: boolean;
  isActive: boolean;
  createdAt: number;
}

@Component({
  selector: 'rdio-scanner-admin-user-groups',
  templateUrl: './user-groups.component.html',
  styleUrls: ['./user-groups.component.scss']
})
export class RdioScannerAdminUserGroupsComponent implements OnInit, OnChanges {
  @Input() form?: FormArray;
  
  groups: UserGroup[] = [];
  loading = false;
  editingGroup: UserGroup | null = null;
  groupForm: FormGroup;
  showCreateForm = false;
  systems: System[] = [];
  selectedSystemAccess: Array<{id: number, talkgroups: number[] | '*'}> = []; // Format with talkgroups
  systemDelayEntries: Array<{systemId: number, delay: number}> = [];
  talkgroupDelayEntries: Array<{systemId: number, talkgroupId: number, delay: number}> = [];
  availableUsers: any[] = [];
  groupAdminMode: 'none' | 'existing' | 'new' = 'none';
  
  // Registration codes management
  selectedGroupForCodes: UserGroup | null = null;
  showCodesDialog = false;
  codes: RegistrationCode[] = [];
  loadingCodes = false;
  newCodeForm = {
    expiresAt: null as Date | null,
    maxUses: 0,
    isOneTime: false
  };
  generatingCode = false;

  // Assign group admin management
  selectedGroupForAdmin: UserGroup | null = null;
  showAssignAdminDialog = false;
  selectedUserIdForAdmin = 0;
  
  // Delete group dialog
  showDeleteGroupDialog = false;
  groupToDelete: UserGroup | null = null;
  usersInGroupToDelete: any[] = [];
  targetGroupIdForMove: number | null = null;
  selectedTargetGroupId: number | null = null;

  // View/manage group admins
  selectedGroupForViewAdmins: UserGroup | null = null;
  showViewAdminsDialog = false;
  groupAdmins: any[] = [];
  loadingGroupAdmins = false;

  // Getter for available groups (excluding the group being deleted)
  get availableGroupsForMove(): UserGroup[] {
    if (!this.groupToDelete) {
      return this.groups;
    }
    return this.groups.filter(g => g.id !== this.groupToDelete!.id);
  }

  constructor(
    private http: HttpClient,
    private snackBar: MatSnackBar,
    private fb: FormBuilder,
    private adminService: RdioScannerAdminService,
    private cdr: ChangeDetectorRef
  ) {
    this.groupForm = this.fb.group({
      id: [0],
      name: ['', Validators.required],
      description: [''],
      systemAccess: [''], // Will be converted to JSON array
      delay: [0],
      systemDelays: [''], // Will be converted to JSON map
      talkgroupDelays: [''], // Will be converted to JSON map
      connectionLimit: [0],
      maxUsers: [0],
      billingEnabled: [false],
      stripePriceId: [''],
      pricingOptions: this.fb.array([]),
      billingMode: ['all_users'],
      collectSalesTax: [false],
      taxMode: ['none'],
      stripeTaxRateId: [''],
      isPublicRegistration: [false],
      allowAddExistingUsers: [false],
      groupAdminUserId: [0],
      newGroupAdminEmail: ['', [Validators.email]],
      newGroupAdminPassword: ['', [Validators.minLength(6)]],
      newGroupAdminFirstName: [''],
      newGroupAdminLastName: [''],
      newGroupAdminZipCode: ['', [Validators.pattern(/^\d{5}(-\d{4})?$/)]]
    });

    // Add at least one pricing option by default
    this.addPricingOption();

    // Set initial validators based on billingEnabled default value
    const initialBillingEnabled = this.groupForm.get('billingEnabled')?.value ?? false;
    this.updatePricingOptionValidators(initialBillingEnabled);

    // Add dynamic validation: at least one pricing option required when billingEnabled is true
    this.groupForm.get('billingEnabled')?.valueChanges.subscribe(billingEnabled => {
      if (billingEnabled && this.pricingOptionsArray.length === 0) {
        this.addPricingOption();
      }
      // Update validators on all pricing options when billingEnabled changes
      this.updatePricingOptionValidators(billingEnabled);
    });
  }

  ngOnInit(): void {
    this.loadGroups();
    this.loadSystems();
    this.loadUsers();
  }

  ngOnChanges(changes: SimpleChanges): void {
    if (changes['form'] && this.form) {
      // When form data is provided (e.g., from import for review), use it
      this.updateGroupsFromForm();
    }
  }

  updateGroupsFromForm(): void {
    if (this.form) {
      this.groups = this.form.value.map((group: any) => ({
        id: group.id || 0,
        name: group.name || '',
        description: group.description || '',
        systemAccess: group.systemAccess || '',
        delay: group.delay || 0,
        systemDelays: group.systemDelays || '',
        talkgroupDelays: group.talkgroupDelays || '',
        connectionLimit: group.connectionLimit || 0,
        maxUsers: group.maxUsers || 0,
        billingEnabled: group.billingEnabled || false,
        stripePriceId: group.stripePriceId || '',
        pricingOptions: group.pricingOptions || [],
        billingMode: group.billingMode || 'all_users',
        collectSalesTax: group.collectSalesTax || false,
        taxMode: group.taxMode || 'none',
        stripeTaxRateId: group.stripeTaxRateId || '',
        isPublicRegistration: group.isPublicRegistration || false,
        allowAddExistingUsers: group.allowAddExistingUsers || false,
        createdAt: group.createdAt || 0
      }));
      this.cdr.detectChanges();
    }
  }

  get pricingOptionsArray(): FormArray {
    return this.groupForm.get('pricingOptions') as FormArray;
  }

  createPricingOptionFormGroup(option?: PricingOption): FormGroup {
    // Don't add required validators initially - they'll be added/removed based on billingEnabled
    return this.fb.group({
      priceId: [option?.priceId || ''],
      label: [option?.label || ''],
      amount: [option?.amount || ''],
      trialDays: [option?.trialDays || 0] // Default to 0 (no trial)
    });
  }

  addPricingOption(): void {
    if (this.pricingOptionsArray.length < 3) {
      this.pricingOptionsArray.push(this.createPricingOptionFormGroup());
      // Apply validators based on current billingEnabled state
      const billingEnabled = this.groupForm.get('billingEnabled')?.value ?? false;
      const validators = billingEnabled ? [Validators.required] : [];
      const newOption = this.pricingOptionsArray.at(this.pricingOptionsArray.length - 1) as FormGroup;
      newOption.get('priceId')?.setValidators(validators);
      newOption.get('priceId')?.updateValueAndValidity();
      newOption.get('label')?.setValidators(validators);
      newOption.get('label')?.updateValueAndValidity();
      newOption.get('amount')?.setValidators(validators);
      newOption.get('amount')?.updateValueAndValidity();
    }
  }

  removePricingOption(index: number): void {
    if (this.pricingOptionsArray.length > 1) {
      this.pricingOptionsArray.removeAt(index);
    }
  }

  updatePricingOptionValidators(billingEnabled: boolean): void {
    const validators = billingEnabled ? [Validators.required] : [];
    this.pricingOptionsArray.controls.forEach(control => {
      const formGroup = control as FormGroup;
      formGroup.get('priceId')?.setValidators(validators);
      formGroup.get('priceId')?.updateValueAndValidity();
      formGroup.get('label')?.setValidators(validators);
      formGroup.get('label')?.updateValueAndValidity();
      formGroup.get('amount')?.setValidators(validators);
      formGroup.get('amount')?.updateValueAndValidity();
    });
  }

  loadUsers(): void {
    this.adminService.getAllUsers().then(users => {
      this.availableUsers = users || [];
    }).catch(error => {
      console.error('Failed to load users:', error);
      this.availableUsers = [];
    });
  }

  loadSystems(): void {
    this.adminService.getConfig().then(config => {
      this.systems = config.systems || [];
    });
  }

  async loadGroups(forceReload: boolean = false): Promise<void> {
    // If form data is provided and not forcing reload, use it instead of loading from backend
    if (this.form && !forceReload) {
      this.updateGroupsFromForm();
      return;
    }

    // Prevent multiple simultaneous requests
    if (this.loading) {
      return;
    }

    this.loading = true;
    
    try {
      const groups = await this.adminService.getAllGroups();
      this.groups = groups;
      
      // Also update the parent form if it exists
      if (this.form) {
        this.form.clear();
        groups.forEach((group: UserGroup) => {
          this.form!.push(this.adminService.newUserGroupForm(group));
        });
      }
      
      this.loading = false;
      this.cdr.detectChanges(); // Trigger change detection
    } catch (error: any) {
      this.loading = false;
      console.error('Failed to load groups:', error);
      this.cdr.detectChanges(); // Trigger change detection even on error
      
      if (error.status === 401) {
        this.snackBar.open('Authentication failed. Please log in again.', 'Close', { duration: 5000 });
      } else {
        const errorMsg = error.error?.message || error.message || 'Failed to load groups';
        this.snackBar.open(errorMsg, 'Close', { duration: 5000 });
      }
    }
  }

  createGroup(): void {
    this.editingGroup = null;
    this.selectedSystemAccess = [];
    this.systemDelayEntries = [];
    this.talkgroupDelayEntries = [];
    this.groupForm.reset({
      id: 0,
      name: '',
      description: '',
      systemAccess: '',
      delay: 0,
      systemDelays: '',
      talkgroupDelays: '',
      connectionLimit: 0,
      maxUsers: 0,
      billingEnabled: false,
      isPublicRegistration: false,
      groupAdminUserId: 0,
      newGroupAdminEmail: '',
      newGroupAdminPassword: '',
      newGroupAdminFirstName: '',
      newGroupAdminLastName: '',
      newGroupAdminZipCode: ''
    });
    this.groupAdminMode = 'none';
    this.showCreateForm = true;
  }
  
  addSystemDelay(): void {
    this.systemDelayEntries.push({ systemId: 0, delay: 0 });
  }
  
  removeSystemDelay(index: number): void {
    this.systemDelayEntries.splice(index, 1);
  }
  
  addTalkgroupDelay(): void {
    this.talkgroupDelayEntries.push({ systemId: 0, talkgroupId: 0, delay: 0 });
  }
  
  removeTalkgroupDelay(index: number): void {
    this.talkgroupDelayEntries.splice(index, 1);
  }

  onTalkgroupDelaySystemSelected(index: number): void {
    // Reset talkgroup selection when system changes
    if (this.talkgroupDelayEntries[index]) {
      this.talkgroupDelayEntries[index].talkgroupId = 0;
    }
    // Trigger change detection to update the talkgroups list
    this.cdr.detectChanges();
  }

  addSystemAccess(): void {
    if (this.selectedSystemAccess.length < this.systems.length) {
      this.selectedSystemAccess.push({
        id: 0,
        talkgroups: '*'
      });
    }
  }

  removeSystemAccess(index: number): void {
    this.selectedSystemAccess.splice(index, 1);
  }

  onSystemSelected(index: number, systemRef: number): void {
    // Reset talkgroup selection to '*' (all talkgroups) when system changes
    if (this.selectedSystemAccess[index]) {
      this.selectedSystemAccess[index].talkgroups = '*';
    }
    // Trigger change detection to update the talkgroups list
    this.cdr.detectChanges();
  }

  getTalkgroupsForSystemAccess(systemRef: number): any[] {
    const system = this.systems.find(s => s.systemRef === systemRef);
    return system?.talkgroups || [];
  }

  toggleTalkgroupInSystem(systemIndex: number, talkgroupId: number, event?: any): void {
    const access = this.selectedSystemAccess[systemIndex];
    if (!access) return;

    // If event is provided (from checkbox), use the checked state
    const shouldSelect = event ? event.checked : !this.isTalkgroupSelected(systemIndex, talkgroupId);

    if (access.talkgroups === '*') {
      if (!shouldSelect) {
        // Switch from all to specific list, excluding this talkgroup
        const system = this.systems.find(s => s.systemRef === access.id);
        if (system && system.talkgroups) {
          access.talkgroups = system.talkgroups
            .map(tg => tg.talkgroupRef)
            .filter((id): id is number => id !== null && id !== undefined && id !== talkgroupId);
        } else {
          access.talkgroups = [];
        }
      }
      // If shouldSelect is true and it's already '*', do nothing
    } else if (Array.isArray(access.talkgroups)) {
      const index = access.talkgroups.indexOf(talkgroupId);
      if (shouldSelect && index < 0) {
        access.talkgroups.push(talkgroupId);
      } else if (!shouldSelect && index >= 0) {
        access.talkgroups.splice(index, 1);
      }
    }
  }

  isTalkgroupSelected(systemIndex: number, talkgroupId: number): boolean {
    const access = this.selectedSystemAccess[systemIndex];
    if (!access) return false;
    if (access.talkgroups === '*') return true;
    if (Array.isArray(access.talkgroups)) {
      return access.talkgroups.includes(talkgroupId);
    }
    return false;
  }

  selectAllTalkgroups(systemIndex: number): void {
    const access = this.selectedSystemAccess[systemIndex];
    if (!access) return;
    access.talkgroups = '*';
  }

  unselectAllTalkgroups(systemIndex: number): void {
    const access = this.selectedSystemAccess[systemIndex];
    if (!access) return;
    access.talkgroups = [];
  }

  hasTalkgroupsSelected(access: {id: number, talkgroups: number[] | '*'}): boolean {
    return access.talkgroups === '*' || (Array.isArray(access.talkgroups) && access.talkgroups.length > 0);
  }

  getTalkgroupCount(access: {id: number, talkgroups: number[] | '*'}): number {
    if (access.talkgroups === '*') {
      return 0; // Will show "All talkgroups" instead
    }
    return Array.isArray(access.talkgroups) ? access.talkgroups.length : 0;
  }

  getTalkgroupsForSystem(systemRef: number): any[] {
    const system = this.systems.find(s => s.systemRef === systemRef);
    return system?.talkgroups || [];
  }

  editGroup(group: UserGroup): void {
    this.editingGroup = group;
    
    // Extract pricingOptions before patching since it's a FormArray, not a simple value
    const pricingOptionsValue = group.pricingOptions;
    
    // Create a copy of group without pricingOptions for patchValue
    const groupForPatch = { ...group };
    delete (groupForPatch as any).pricingOptions;
    
    // Patch the form (excluding pricingOptions which we'll handle separately)
    this.groupForm.patchValue(groupForPatch);
    
    // Parse system access JSON - support both legacy (array of IDs) and new format (array of objects)
    try {
      // Handle empty string or whitespace
      const trimmed = (group.systemAccess || '').trim();
      const parsed = trimmed ? JSON.parse(trimmed) : [];
      if (Array.isArray(parsed) && parsed.length > 0) {
        // Check if it's new format (objects with id and talkgroups)
        // Add null check before hasOwnProperty
        if (typeof parsed[0] === 'object' && parsed[0] !== null && parsed[0].hasOwnProperty('id')) {
          this.selectedSystemAccess = parsed;
        } else {
          // Legacy format (simple array of IDs) - convert to new format
          this.selectedSystemAccess = parsed.map(id => ({
            id: id,
            talkgroups: '*' as const
          }));
        }
      } else {
        this.selectedSystemAccess = [];
      }
    } catch (e) {
      console.error('Error parsing systemAccess:', e);
      this.selectedSystemAccess = [];
    }
    
    // Parse system delays JSON
    try {
      const systemDelaysMap = group.systemDelays ? JSON.parse(group.systemDelays) : {};
      this.systemDelayEntries = Object.keys(systemDelaysMap).map(key => ({
        systemId: parseInt(key),
        delay: systemDelaysMap[key]
      }));
    } catch {
      this.systemDelayEntries = [];
    }
    
    // Parse talkgroup delays JSON
    try {
      const talkgroupDelaysMap = group.talkgroupDelays ? JSON.parse(group.talkgroupDelays) : {};
      this.talkgroupDelayEntries = Object.keys(talkgroupDelaysMap).map(key => {
        const [systemId, talkgroupId] = key.split(':').map(Number);
        return {
          systemId,
          talkgroupId,
          delay: talkgroupDelaysMap[key]
        };
      });
    } catch {
      this.talkgroupDelayEntries = [];
    }
    
    // Clear and populate pricing options
    this.pricingOptionsArray.clear();
    try {
      // Parse pricingOptions if it's a JSON string
      const pricingOptions = typeof pricingOptionsValue === 'string' 
        ? JSON.parse(pricingOptionsValue) 
        : (pricingOptionsValue || []);
      
      if (Array.isArray(pricingOptions) && pricingOptions.length > 0) {
        pricingOptions.forEach(option => {
          this.pricingOptionsArray.push(this.createPricingOptionFormGroup(option));
        });
      } else {
        // Add one empty pricing option if none exist
        this.addPricingOption();
      }
    } catch (error) {
      console.error('Error parsing pricingOptions:', error);
      // Add one empty pricing option if parsing fails
      this.addPricingOption();
    }
    
    // Update validators based on the group's billingEnabled value
    const billingEnabled = group.billingEnabled ?? false;
    this.updatePricingOptionValidators(billingEnabled);
    
    this.showCreateForm = true;
  }

  saveGroup(): void {
    // Validate group admin fields if creating new user
    if (!this.editingGroup && this.groupAdminMode === 'new') {
      const email = this.groupForm.get('newGroupAdminEmail')?.value;
      const password = this.groupForm.get('newGroupAdminPassword')?.value;
      const firstName = this.groupForm.get('newGroupAdminFirstName')?.value;
      const lastName = this.groupForm.get('newGroupAdminLastName')?.value;
      const zipCode = this.groupForm.get('newGroupAdminZipCode')?.value;

      if (!email || !password || !firstName || !lastName || !zipCode) {
        this.snackBar.open('All fields are required when creating a new group admin', 'Close', { duration: 3000 });
        return;
      }

      if (password.length < 6) {
        this.snackBar.open('Password must be at least 6 characters', 'Close', { duration: 3000 });
        return;
      }
    }

    // Validate existing user selection if assigning existing user
    if (!this.editingGroup && this.groupAdminMode === 'existing') {
      const userId = this.groupForm.get('groupAdminUserId')?.value;
      if (!userId || userId === 0) {
        this.snackBar.open('Please select a user to assign as group admin', 'Close', { duration: 3000 });
        return;
      }
    }

    // Additional validation: If billing is enabled, at least one pricing option must be set
    if (this.groupForm.get('billingEnabled')?.value && this.pricingOptionsArray.length === 0) {
      this.snackBar.open('At least one pricing option is required when billing is enabled', 'Close', { duration: 3000 });
      return;
    }
    
    // Validate all pricing options have required fields
    if (this.groupForm.get('billingEnabled')?.value) {
      for (let i = 0; i < this.pricingOptionsArray.length; i++) {
        const option = this.pricingOptionsArray.at(i);
        if (!option.get('priceId')?.value || !option.get('label')?.value || !option.get('amount')?.value) {
          this.snackBar.open(`Pricing option ${i + 1} is missing required fields`, 'Close', { duration: 3000 });
          return;
        }
      }
    }

    if (this.groupForm.invalid) {
      return;
    }

    const groupData = this.groupForm.value;
    
    // Filter out empty pricing options - only include complete ones
    if (groupData.pricingOptions && Array.isArray(groupData.pricingOptions)) {
      groupData.pricingOptions = groupData.pricingOptions.filter((opt: any) => 
        opt.priceId && opt.label && opt.amount
      );
    }
    
    // If billing is disabled, don't send pricing options at all
    if (!groupData.billingEnabled) {
      groupData.pricingOptions = [];
    }
    
    // Convert system access to JSON - always use talkgroup format
    groupData.systemAccess = this.selectedSystemAccess.length > 0 
      ? JSON.stringify(this.selectedSystemAccess) 
      : '';
    
    // Convert system delays to JSON map
    const systemDelaysMap: {[key: number]: number} = {};
    this.systemDelayEntries.forEach(entry => {
      if (entry.systemId && entry.delay >= 0) {
        systemDelaysMap[entry.systemId] = entry.delay;
      }
    });
    groupData.systemDelays = Object.keys(systemDelaysMap).length > 0
      ? JSON.stringify(systemDelaysMap)
      : '';
    
    // Convert talkgroup delays to JSON map
    const talkgroupDelaysMap: {[key: string]: number} = {};
    this.talkgroupDelayEntries.forEach(entry => {
      if (entry.systemId && entry.talkgroupId && entry.delay >= 0) {
        talkgroupDelaysMap[`${entry.systemId}:${entry.talkgroupId}`] = entry.delay;
      }
    });
    groupData.talkgroupDelays = Object.keys(talkgroupDelaysMap).length > 0
      ? JSON.stringify(talkgroupDelaysMap)
      : '';
    
    if (groupData.id > 0) {
      // Update existing group
      this.http.put('/api/admin/groups/update', groupData, { headers: this.adminService.getAuthHeaders() }).subscribe({
        next: async () => {
          this.snackBar.open('Group updated successfully', 'Close', { duration: 3000 });
          this.showCreateForm = false;
          // Force reload from backend to get the updated group and update parent form
          await this.loadGroups(true);
          // Mark parent form as dirty so user knows to save
          if (this.form && this.form.parent) {
            this.form.parent.markAsDirty();
          }
          this.cdr.detectChanges();
        },
        error: (error) => {
          console.error('Failed to update group:', error);
          this.snackBar.open(error.error?.message || 'Failed to update group', 'Close', { duration: 3000 });
        }
      });
    } else {
      // Create new group
      // Add group admin information if specified
      if (this.groupAdminMode === 'existing' && groupData.groupAdminUserId > 0) {
        groupData.assignExistingUserAsAdmin = true;
        groupData.groupAdminUserId = groupData.groupAdminUserId;
      } else if (this.groupAdminMode === 'new') {
        groupData.createNewUserAsAdmin = true;
        groupData.newGroupAdminEmail = groupData.newGroupAdminEmail;
        groupData.newGroupAdminPassword = groupData.newGroupAdminPassword;
        groupData.newGroupAdminFirstName = groupData.newGroupAdminFirstName;
        groupData.newGroupAdminLastName = groupData.newGroupAdminLastName;
        groupData.newGroupAdminZipCode = groupData.newGroupAdminZipCode;
      }

      this.http.post('/api/admin/groups/create', groupData, { headers: this.adminService.getAuthHeaders() }).subscribe({
        next: () => {
          this.snackBar.open('Group created successfully', 'Close', { duration: 3000 });
          this.showCreateForm = false;
          // Force reload from backend to get the new group and update parent form
          this.loadGroups(true);
        },
        error: (error) => {
          console.error('Failed to create group:', error);
          this.snackBar.open(error.error?.message || 'Failed to create group', 'Close', { duration: 3000 });
        }
      });
    }
  }

  cancelEdit(): void {
    this.showCreateForm = false;
    this.editingGroup = null;
    this.selectedSystemAccess = [];
    this.groupForm.reset();
    this.groupAdminMode = 'none';
    this.systemDelayEntries = [];
    this.talkgroupDelayEntries = [];
  }

  async deleteGroup(groupId: number): Promise<void> {
    const group = this.groups.find(g => g.id === groupId);
    if (!group) return;
    
    // Check if there are users in this group
    let usersInGroup: any[] = [];
    try {
      const allUsers = await this.adminService.getAllUsers();
      usersInGroup = allUsers.filter((u: any) => u.userGroupId === groupId);
    } catch (error) {
      console.error('Failed to check users in group:', error);
    }
    
    if (usersInGroup.length > 0) {
      // Show dialog with group selection
      this.groupToDelete = group;
      this.usersInGroupToDelete = usersInGroup;
      this.targetGroupIdForMove = null;
      this.selectedTargetGroupId = null;
      this.showDeleteGroupDialog = true;
    } else {
      // No users, just confirm and delete
      if (!confirm(`Are you sure you want to delete group "${group.name}"?`)) {
        return;
      }
      this.executeGroupDelete(groupId, null);
    }
  }
  
  executeGroupDelete(groupId: number, targetGroupId: number | null): void {
    // If target group selected, move users first
    if (targetGroupId && targetGroupId > 0 && this.usersInGroupToDelete.length > 0) {
      Promise.all(
        this.usersInGroupToDelete.map((user: any) => {
          // Send all required fields for user update - backend requires non-empty strings
          const userUpdateData = {
            email: user.email || '',
            firstName: user.firstName || '',
            lastName: user.lastName || '',
            zipCode: user.zipCode || '',
            verified: user.verified || false,
            systems: user.systems || '',
            delay: user.delay || 0,
            userGroupId: targetGroupId
          };
          // Validate required fields before sending
          if (!userUpdateData.email || !userUpdateData.firstName || !userUpdateData.lastName || !userUpdateData.zipCode) {
            console.error('User missing required fields:', user);
            throw new Error(`User ${user.id} is missing required fields (email, firstName, lastName, or zipCode)`);
          }
          return this.adminService.updateUser(user.id, userUpdateData);
        })
      ).then(() => {
        this.performGroupDelete(groupId);
      }).catch(error => {
        console.error('Failed to move users:', error);
        const errorMsg = error?.error?.error || error?.message || 'Failed to move some users';
        this.snackBar.open(errorMsg, 'Close', { duration: 5000 });
      });
    } else if (targetGroupId === -2) {
      // Delete users option selected
      Promise.all(
        this.usersInGroupToDelete.map((user: any) => 
          this.adminService.deleteUser(user.id)
        )
      ).then(() => {
        this.performGroupDelete(groupId);
      }).catch(error => {
        console.error('Failed to delete users:', error);
        this.snackBar.open('Failed to delete some users', 'Close', { duration: 3000 });
      });
    } else {
      // Unassign users (targetGroupId is null) - set userGroupId to 0
      Promise.all(
        this.usersInGroupToDelete.map((user: any) => {
          const userUpdateData = {
            email: user.email || '',
            firstName: user.firstName || '',
            lastName: user.lastName || '',
            zipCode: user.zipCode || '',
            verified: user.verified || false,
            systems: user.systems || '',
            delay: user.delay || 0,
            userGroupId: 0  // Use 0 to unassign (backend accepts 0 as unassigned)
          };
          // Validate required fields before sending
          if (!userUpdateData.email || !userUpdateData.firstName || !userUpdateData.lastName || !userUpdateData.zipCode) {
            console.error('User missing required fields:', user);
            throw new Error(`User ${user.id} is missing required fields (email, firstName, lastName, or zipCode)`);
          }
          return this.adminService.updateUser(user.id, userUpdateData);
        })
      ).then(() => {
        this.performGroupDelete(groupId);
      }).catch(error => {
        console.error('Failed to unassign users:', error);
        const errorMsg = error?.error?.error || error?.message || 'Failed to unassign some users';
        this.snackBar.open(errorMsg, 'Close', { duration: 5000 });
      });
    }
  }
  
  applyTaxRateToExistingSubscriptions(group: UserGroup): void {
    if (!confirm(`Apply the fixed tax rate to all active subscribers in "${group.name}"?\n\nThis will update their Stripe subscriptions so the tax rate is charged on their next invoice.`)) {
      return;
    }
    this.http.post('/api/admin/groups/apply-tax-rate', { groupId: group.id }, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: (response: any) => {
        const msg = `Tax rate applied: ${response.updated} updated, ${response.failed} failed, ${response.skipped} skipped.`;
        this.snackBar.open(msg, 'Close', { duration: 6000 });
        if (response.failed > 0) {
          console.error('Some subscriptions failed to update:', response.results?.filter((r: any) => r.status === 'failed'));
        }
      },
      error: (error) => {
        console.error('Failed to apply tax rate:', error);
        this.snackBar.open(error.error?.message || error.error?.error || 'Failed to apply tax rate to subscriptions', 'Close', { duration: 5000 });
      }
    });
  }

  performGroupDelete(groupId: number): void {
    this.http.delete(`/api/admin/groups/delete/${groupId}`, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: () => {
        this.snackBar.open('Group deleted successfully', 'Close', { duration: 3000 });
        this.showDeleteGroupDialog = false;
        // Force reload from backend and sync parent form
        this.loadGroups(true).then(() => {
          if (this.form && this.form.parent) {
            this.form.parent.markAsDirty();
          }
        });
      },
      error: (error) => {
        console.error('Failed to delete group:', error);
        const errorMsg = error.error?.error || error.error?.message || 'Failed to delete group';
        this.snackBar.open(errorMsg, 'Close', { duration: 5000 });
        this.showDeleteGroupDialog = false;
      }
    });
  }
  
  onDeleteOptionChange(): void {
    // Reset selected target group when switching options
    if (this.targetGroupIdForMove !== -1) {
      this.selectedTargetGroupId = null;
    }
  }
  
  onTargetGroupSelected(event: any): void {
    // When a group is selected from dropdown, update targetGroupIdForMove
    // But keep the radio button selected by setting it to the selected group ID
    // We'll handle this in confirmGroupDelete by checking selectedTargetGroupId
  }
  
  confirmGroupDelete(): void {
    if (this.groupToDelete) {
      // If "Move users" is selected and a group has been chosen, use selectedTargetGroupId
      let finalTargetId = this.targetGroupIdForMove;
      if (this.targetGroupIdForMove === -1 && this.selectedTargetGroupId) {
        finalTargetId = this.selectedTargetGroupId;
      }
      this.executeGroupDelete(this.groupToDelete.id, finalTargetId);
    }
  }
  
  cancelGroupDelete(): void {
    this.showDeleteGroupDialog = false;
    this.groupToDelete = null;
    this.usersInGroupToDelete = [];
    this.targetGroupIdForMove = null;
    this.selectedTargetGroupId = null;
  }

  openAssignAdminDialog(group: UserGroup): void {
    this.selectedGroupForAdmin = group;
    this.selectedUserIdForAdmin = 0;
    this.showAssignAdminDialog = true;
    // Reload users to ensure we have the latest list
    this.loadUsers();
  }

  closeAssignAdminDialog(): void {
    this.showAssignAdminDialog = false;
    this.selectedGroupForAdmin = null;
    this.selectedUserIdForAdmin = 0;
  }

  assignGroupAdmin(): void {
    if (!this.selectedGroupForAdmin || this.selectedUserIdForAdmin === 0) {
      this.snackBar.open('Please select a user', 'Close', { duration: 3000 });
      return;
    }

    this.http.post('/api/admin/groups/assign-admin', 
      { userId: this.selectedUserIdForAdmin, groupId: this.selectedGroupForAdmin.id }, 
      { headers: this.adminService.getAuthHeaders() }
    ).subscribe({
      next: () => {
        this.snackBar.open('Group admin assigned successfully', 'Close', { duration: 3000 });
        this.closeAssignAdminDialog();
        this.loadGroups(); // Reload groups to show updated state
        this.loadUsers(); // Reload users to show updated admin status
        // Reload group admins if the view admins dialog is open
        if (this.showViewAdminsDialog && this.selectedGroupForViewAdmins) {
          this.loadGroupAdmins();
        }
      },
      error: (error) => {
        console.error('Failed to assign group admin:', error);
        this.snackBar.open(error.error?.message || 'Failed to assign group admin', 'Close', { duration: 3000 });
      }
    });
  }

  openViewAdminsDialog(group: UserGroup): void {
    this.selectedGroupForViewAdmins = group;
    this.showViewAdminsDialog = true;
    this.loadGroupAdmins();
  }

  closeViewAdminsDialog(): void {
    this.showViewAdminsDialog = false;
    this.selectedGroupForViewAdmins = null;
    this.groupAdmins = [];
  }

  loadGroupAdmins(): void {
    if (!this.selectedGroupForViewAdmins) {
      return;
    }

    this.loadingGroupAdmins = true;
    this.http.get(`/api/admin/groups/admins?groupId=${this.selectedGroupForViewAdmins.id}`, 
      { headers: this.adminService.getAuthHeaders() }
    ).subscribe({
      next: (response: any) => {
        this.loadingGroupAdmins = false;
        this.groupAdmins = response.groupAdmins || [];
      },
      error: (error) => {
        this.loadingGroupAdmins = false;
        console.error('Failed to load group admins:', error);
        this.snackBar.open('Failed to load group admins', 'Close', { duration: 3000 });
      }
    });
  }

  removeGroupAdmin(adminId: number): void {
    if (!this.selectedGroupForViewAdmins) {
      return;
    }

    if (!confirm('Are you sure you want to remove this user as a group admin?')) {
      return;
    }

    this.http.post('/api/admin/groups/remove-admin', 
      { userId: adminId, groupId: this.selectedGroupForViewAdmins.id }, 
      { headers: this.adminService.getAuthHeaders() }
    ).subscribe({
      next: () => {
        this.snackBar.open('Group admin removed successfully', 'Close', { duration: 3000 });
        this.loadGroupAdmins(); // Reload the list
        this.loadUsers(); // Reload users to show updated admin status
      },
      error: (error) => {
        console.error('Failed to remove group admin:', error);
        this.snackBar.open(error.error?.message || 'Failed to remove group admin', 'Close', { duration: 3000 });
      }
    });
  }

  openCodesDialog(group: UserGroup): void {
    this.selectedGroupForCodes = group;
    this.showCodesDialog = true;
    this.loadCodes();
  }

  closeCodesDialog(): void {
    this.showCodesDialog = false;
    this.selectedGroupForCodes = null;
    this.codes = [];
  }

  loadCodes(): void {
    if (!this.selectedGroupForCodes) return;
    
    this.loadingCodes = true;
    this.http.get(`/api/admin/groups/${this.selectedGroupForCodes.id}/codes`, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: (response: any) => {
        this.loadingCodes = false;
        this.codes = response.codes || [];
      },
      error: (error) => {
        this.loadingCodes = false;
        console.error('Failed to load codes:', error);
        this.snackBar.open('Failed to load registration codes', 'Close', { duration: 3000 });
      }
    });
  }

  generateCode(): void {
    if (!this.selectedGroupForCodes) return;
    
    this.generatingCode = true;
    
    // Convert date to Unix timestamp (seconds since epoch)
    let expiresAt = 0;
    if (this.newCodeForm.expiresAt) {
      expiresAt = Math.floor(this.newCodeForm.expiresAt.getTime() / 1000);
    }
    
    const payload = {
      expiresAt: expiresAt,
      maxUses: this.newCodeForm.maxUses > 0 ? this.newCodeForm.maxUses : 0,
      isOneTime: this.newCodeForm.isOneTime
    };

    this.http.post(`/api/admin/groups/${this.selectedGroupForCodes.id}/codes/generate`, payload, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: (response: any) => {
        this.generatingCode = false;
        this.snackBar.open(`Code generated: ${response.code}`, 'Close', { duration: 5000 });
        this.newCodeForm = { expiresAt: null, maxUses: 0, isOneTime: false };
        this.loadCodes();
      },
      error: (error) => {
        this.generatingCode = false;
        console.error('Failed to generate code:', error);
        this.snackBar.open(error.error?.message || 'Failed to generate code', 'Close', { duration: 3000 });
      }
    });
  }

  formatDate(timestamp: number): string {
    if (!timestamp || timestamp === 0) return 'Never';
    return new Date(timestamp * 1000).toLocaleString();
  }

  deleteCode(codeId: number): void {
    if (!this.selectedGroupForCodes) return;
    
    if (!confirm('Are you sure you want to delete this registration code? This action cannot be undone.')) {
      return;
    }

    this.http.delete(`/api/admin/groups/${this.selectedGroupForCodes.id}/codes/${codeId}`, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: () => {
        this.snackBar.open('Registration code deleted successfully', 'Close', { duration: 3000 });
        this.loadCodes();
      },
      error: (error) => {
        console.error('Failed to delete code:', error);
        this.snackBar.open(error.error?.message || 'Failed to delete registration code', 'Close', { duration: 3000 });
      }
    });
  }
}

