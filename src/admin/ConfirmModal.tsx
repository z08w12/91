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
  centerMessage?: boolean;
  modalClassName?: string;
  loading?: boolean;
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
  centerMessage = false,
  modalClassName = "",
  loading = false,
  onCancel,
  onConfirm,
}: ConfirmModalProps) {
  return (
    <Modal
      open={open}
      title={title}
      onClose={onCancel}
      className={modalClassName}
      footer={
        <>
          <button type="button" className="admin-btn" onClick={onCancel} disabled={loading}>
            {cancelText}
          </button>
          <button
            type="button"
            className={`admin-btn${danger ? " is-danger" : " is-primary"}`}
            onClick={onConfirm}
            disabled={loading}
          >
            {loading ? "处理中..." : confirmText}
          </button>
        </>
      }
    >
      <div className={`admin-confirm${centerMessage ? " is-message-centered" : ""}`}>
        <div className={`admin-confirm__icon${danger ? " is-danger" : ""}`} aria-hidden={centerMessage}>
          <AlertTriangle size={20} />
        </div>
        <div className="admin-confirm__content">
          <p className="admin-confirm__message">{message}</p>
          {details && details.length > 0 && (
            <ul className="admin-confirm__list">
              {details.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </Modal>
  );
}
