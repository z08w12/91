import { useId, useMemo, useState } from "react";
import { ArrowLeft, ChevronDown } from "lucide-react";
import { P123QRCodeLogin } from "./P123QRCodeLogin";
import { Spider91UploadTargetField } from "./Spider91UploadTargetField";
import {
  FormState,
  Kind,
  credentialFields,
  credentialHelp,
  usesRootDirectoryID,
  rootIdPlaceholder,
} from "./constants";
import * as api from "../api";

type DriveOption = {
  kind: Kind;
  label: string;
  abbr: string;
  desc: string;
};

const DRIVE_OPTIONS: DriveOption[] = [
  { kind: "p115", label: "115 网盘", abbr: "115", desc: "302直链，不占带宽" },
  { kind: "p123", label: "123 云盘", abbr: "123", desc: "扫码登录，302直链" },
  { kind: "pikpak", label: "PikPak", abbr: "Pk", desc: "302直链，稳定快速" },
  { kind: "onedrive", label: "OneDrive", abbr: "OD", desc: "302直链，微软网盘" },
  { kind: "googledrive", label: "Google Drive", abbr: "GD", desc: "服务器中转模式" },
  { kind: "localstorage", label: "本地存储", abbr: "Lo", desc: "本机文件目录" },
  { kind: "spider91", label: "91 爬虫", abbr: "91", desc: "自动抓取热门视频" },
  { kind: "quark", label: "夸克网盘", abbr: "Qk", desc: "302直链" },
  { kind: "wopan", label: "联通沃盘", abbr: "Wo", desc: "302直链" },
];

export function DriveForm({
  form,
  onChange,
  isEdit,
  uploadTargets,
  nameError,
  onNameBlur,
  onBack,
}: {
  form: FormState;
  onChange: (f: FormState) => void;
  isEdit: boolean;
  uploadTargets: api.AdminDrive[];
  nameError?: string;
  onNameBlur?: () => void;
  onBack?: () => void;
}) {
  const idPrefix = useId();
  const fields = useMemo(() => credentialFields(form.kind, form.creds), [form.kind, form.creds]);
  const help = credentialHelp(form.kind, isEdit);
  const [step, setStep] = useState<"type" | "form">(isEdit ? "form" : "type");
  const nameId = `${idPrefix}-drive-name`;
  const rootId = `${idPrefix}-drive-root`;

  function set<K extends keyof FormState>(k: K, v: FormState[K]) {
    onChange({ ...form, [k]: v });
  }
  function setCred(k: string, v: string) {
    onChange({ ...form, creds: { ...form.creds, [k]: v } });
  }
  function setKind(v: Kind) {
    onChange({
      ...form,
      kind: v,
      rootId: "",
      creds: {},
    });
  }
  function selectType(kind: Kind) {
    setKind(kind);
    setStep("form");
  }
  function goBack() {
    setStep("type");
    onChange({
      ...form,
      name: "",
      rootId: "",
      creds: {},
    });
    onBack?.();
  }

  const selectedOption = DRIVE_OPTIONS.find((o) => o.kind === form.kind);

  if (step === "type" && !isEdit) {
    return (
      <div className="admin-drive-type-picker">
        <div className="admin-drive-type-grid">
          {DRIVE_OPTIONS.map((opt) => (
            <button
              key={opt.kind}
              type="button"
              className="admin-drive-type-card"
              data-kind={opt.kind}
              onClick={() => selectType(opt.kind)}
            >
              <span className="admin-drive-type-card__icon" data-kind={opt.kind}>
                {opt.abbr}
              </span>
              <span className="admin-drive-type-card__label">{opt.label}</span>
            </button>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="admin-form">
      {!isEdit && selectedOption && (
        <div className="admin-drive-selected-bar" data-kind={form.kind}>
          <span className="admin-drive-selected-bar__icon" data-kind={form.kind}>
            {selectedOption.abbr}
          </span>
          <div className="admin-drive-selected-bar__text">
            <span className="admin-drive-selected-bar__name">{selectedOption.label}</span>
            <span className="admin-drive-selected-bar__desc">{selectedOption.desc}</span>
          </div>
          <button type="button" className="admin-drive-selected-bar__back" onClick={goBack}>
            <ArrowLeft size={12} /> 重选类型
          </button>
        </div>
      )}

      <div className="admin-form__section">
        <div className="admin-form__row">
          <label htmlFor={nameId}>名称 *</label>
          <input
            id={nameId}
            value={form.name}
            onChange={(e) => set("name", e.target.value)}
            onBlur={onNameBlur}
            placeholder="给这个盘起个名字"
            className={nameError ? "is-invalid" : undefined}
            aria-invalid={nameError ? "true" : undefined}
            aria-describedby={nameError ? `${nameId}-error` : undefined}
          />
          {nameError && (
            <div className="admin-form__error" id={`${nameId}-error`}>
              {nameError}
            </div>
          )}
        </div>

        {usesRootDirectoryID(form.kind) && (
          <div className="admin-form__row">
            <label htmlFor={rootId}>根目录 ID</label>
            <input
              id={rootId}
              value={form.rootId}
              onChange={(e) => set("rootId", e.target.value)}
              placeholder={rootIdPlaceholder(form.kind)}
            />
            <div className="admin-form__help">
              留空时使用该网盘类型的默认根目录
            </div>
          </div>
        )}
      </div>

      {(help || fields.length > 0) && (
        <div className="admin-form__section">
          <h3 className="admin-form__section-label">凭证配置</h3>

          {help && (
            <div className="admin-form__help admin-form__help--lead">
              {help}
            </div>
          )}

          {form.kind === "p123" && (
            <P123QRCodeLogin
              onToken={(token) => setCred("access_token", token)}
            />
          )}

          {fields.map((f) => (
            <div key={f.key} className="admin-form__row">
              {f.type === "select" ? (
                <>
                  <label htmlFor={`${idPrefix}-credential-${f.key}`}>
                    {f.label}
                    {f.required && " *"}
                  </label>
                  <div className="admin-form-select-wrap">
                    <select
                      id={`${idPrefix}-credential-${f.key}`}
                      className="admin-form-select"
                      value={form.creds[f.key] ?? f.defaultValue ?? ""}
                      onChange={(e) => setCred(f.key, e.target.value)}
                    >
                      {(f.options ?? []).map((option) => (
                        <option key={option.value} value={option.value}>
                          {option.label}
                        </option>
                      ))}
                    </select>
                    <ChevronDown size={15} className="admin-form-select__icon" aria-hidden="true" />
                  </div>
                </>
              ) : (
                <>
                  <label htmlFor={`${idPrefix}-credential-${f.key}`}>
                    {f.label}
                    {f.required && " *"}
                  </label>
                  {f.multiline ? (
                    <textarea
                      id={`${idPrefix}-credential-${f.key}`}
                      value={form.creds[f.key] ?? ""}
                      onChange={(e) => setCred(f.key, e.target.value)}
                      placeholder={f.placeholder}
                      required={f.required && !isEdit}
                    />
                  ) : (
                    <input
                      id={`${idPrefix}-credential-${f.key}`}
                      type={credentialInputType(f.key)}
                      value={form.creds[f.key] ?? ""}
                      onChange={(e) => setCred(f.key, e.target.value)}
                      placeholder={f.placeholder}
                      required={f.required && !isEdit}
                    />
                  )}
                </>
              )}
              {f.help && <div className="admin-form__help">{f.help}</div>}
            </div>
          ))}
        </div>
      )}

      {form.kind === "spider91" && (
        <div className="admin-form__section">
          <h3 className="admin-form__section-label">上传设置</h3>
          <Spider91UploadTargetField
            value={form.spider91UploadDriveId}
            onChange={(v) => set("spider91UploadDriveId", v)}
            uploadTargets={uploadTargets}
          />
        </div>
      )}
    </div>
  );
}

function credentialInputType(key: string): string {
  return /password|token|secret/i.test(key) ? "password" : "text";
}
