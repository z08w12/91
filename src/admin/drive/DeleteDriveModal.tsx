import { Trash2 } from "lucide-react";
import * as api from "../api";
import { Modal } from "../Modal";

export function DeleteDriveModal({
  drive,
  deleting,
  onCancel,
  onConfirm,
}: {
  drive: api.AdminDrive | null;
  deleting: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const name = drive?.name || drive?.id || "";
  const isSpider91 = drive?.kind === "spider91";
  const title = isSpider91 ? "删除 91Spider" : "删除存储";
  const primaryText = deleting ? "删除中..." : "确认删除";

  return (
    <Modal
      open={!!drive}
      title={title}
      onClose={onCancel}
      className="admin-modal--delete-confirm"
      footer={
        <>
          <button className="admin-btn" onClick={onCancel} disabled={deleting}>
            取消
          </button>
          <button className="admin-btn is-danger" onClick={onConfirm} disabled={deleting}>
            <Trash2 size={13} />
            {primaryText}
          </button>
        </>
      }
    >
      <div className="admin-confirm is-message-centered">
        <div className="admin-confirm__content">
          <p className="admin-confirm__message">{`确定要删除「${name}」吗？`}</p>
        </div>
      </div>
    </Modal>
  );
}
