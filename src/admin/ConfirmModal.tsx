import type { ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import { Modal } from "./Modal";

type ConfirmModalProps = {
  open: boolean;
  title: string;
  message: string;
  details?: string[];
  confirmText?: string;
  cancelText?: string;
  danger?: boolean;
  plainConfirm?: boolean;
  hideIcon?: boolean;
  centerMessage?: boolean;
  modalClassName?: string;
  loading?: boolean;
  restoreFocus?: boolean;
  children?: ReactNode;
  onCancel: () => void;
  onConfirm: () => void;
};

export function ConfirmModal({
  open,
  title,
  message,
  details,
  confirmText = "确认",
  cancelText = "取消",
  danger = false,
  plainConfirm = false,
  hideIcon = false,
  centerMessage = false,
  modalClassName = "",
  loading = false,
  restoreFocus = true,
  children,
  onCancel,
  onConfirm,
}: ConfirmModalProps) {
  return (
    <Modal
      open={open}
      title={title}
      onClose={onCancel}
      className={`admin-modal--confirm${modalClassName ? ` ${modalClassName}` : ""}`}
      restoreFocus={restoreFocus}
      footer={
        <>
          <button type="button" className="admin-btn" onClick={onCancel} disabled={loading}>
            {cancelText}
          </button>
          <button
            type="button"
            className={`admin-btn${plainConfirm ? "" : danger ? " is-danger" : " is-primary"}`}
            onClick={onConfirm}
            disabled={loading}
          >
            {loading ? "处理中..." : confirmText}
          </button>
        </>
      }
    >
      <div className={`admin-confirm${centerMessage ? " is-message-centered" : ""}${hideIcon ? " has-no-icon" : ""}`}>
        {!hideIcon && (
          <div className={`admin-confirm__icon${danger ? " is-danger" : ""}`} aria-hidden={centerMessage}>
            <AlertTriangle size={20} />
          </div>
        )}
        <div className="admin-confirm__content">
          <p className="admin-confirm__message">{message}</p>
          {details && details.length > 0 && (
            <ul className="admin-confirm__list">
              {details.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          )}
          {children}
        </div>
      </div>
    </Modal>
  );
}
