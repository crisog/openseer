'use client';

import React, { useState } from 'react';
import { Loader2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

interface DeleteMonitorModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
  monitorName: string;
  isDeleting?: boolean;
}

export function DeleteMonitorModal({
  open,
  onOpenChange,
  onConfirm,
  monitorName,
  isDeleting = false
}: DeleteMonitorModalProps): React.JSX.Element {
  const [confirmText, setConfirmText] = useState('');

  const isConfirmValid = confirmText === 'delete';

  const handleConfirm = () => {
    if (isConfirmValid) {
      onConfirm();
    }
  };

  const handleOpenChange = (newOpen: boolean) => {
    if (!isDeleting) {
      setConfirmText('');
      onOpenChange(newOpen);
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="surface max-w-md">
        <DialogHeader>
          <DialogTitle>Delete Monitor</DialogTitle>
          <DialogDescription>
            Are you sure you want to delete "{monitorName}"? This action cannot be undone.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-2">
          <Label htmlFor="confirm-text">
            Type "delete" to confirm:
          </Label>
          <Input
            id="confirm-text"
            value={confirmText}
            onChange={(e) => setConfirmText(e.target.value)}
            placeholder="delete"
            disabled={isDeleting}
          />
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => handleOpenChange(false)}
            disabled={isDeleting}
          >
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={handleConfirm}
            disabled={!isConfirmValid || isDeleting}
          >
            {isDeleting ? (
              <>
                <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                Deleting...
              </>
            ) : (
              'Delete'
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}