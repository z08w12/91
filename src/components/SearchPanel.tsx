import { FormEvent, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Search } from "lucide-react";

export function SearchPanel() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [keyword, setKeyword] = useState(params.get("q") ?? "");

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const q = keyword.trim();
    const sp = new URLSearchParams();
    if (q) sp.set("q", q);
    navigate(`/list?${sp.toString()}`);
  }

  return (
    <form className="search-panel" onSubmit={handleSubmit} role="search">
      <div className="search-panel__form">
        <input
          className="search-panel__input"
          type="text"
          value={keyword}
          onChange={(e) => setKeyword(e.target.value)}
          placeholder="搜索视频标题或作者"
          aria-label="搜索关键词"
        />
        <button className="search-panel__submit" type="submit">
          <Search size={16} />
          搜索
        </button>
      </div>
    </form>
  );
}
